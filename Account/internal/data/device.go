package data

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Jesse-467/im/Account/internal/biz"
	"github.com/Jesse-467/im/Account/internal/conf"
)

// 编译期断言：设备存储必须满足业务层声明的接口。
var _ biz.DeviceStore = (*deviceStore)(nil)

// deviceKeyPrefix 是「用户在线设备」有序集合的键前缀。
//
// 结构为 ZSET：member = device_key，score = 上线时间（Unix 毫秒）。
//
// 为什么用有序集合而不是集合 / 列表：
//   - 需要「按上线时间淘汰最早的设备」，有序集合按 score 排序天然满足，
//     ZPOPMIN 一次即可取出最早的那台；集合无序、列表无法按时间排序，
//     两者都做不到这一点；
//   - 需要「同一设备重复登录只占一个位置」，有序集合以 member 去重，
//     重新登录只是更新 score，不会新增成员。
const deviceKeyPrefix = "im:online:devices:"

// deviceKey 生成某用户的设备有序集合键。
func deviceKey(userID int64) string {
	return deviceKeyPrefix + strconv.FormatInt(userID, 10)
}

// DeviceTTL 是设备在线标记的兜底有效期。
//
// 用具名类型而不是裸 time.Duration：Wire 按类型匹配依赖，
// 裸 Duration 会与其他任何需要时长的组件冲突。
type DeviceTTL time.Duration

// deviceStore 是 biz.DeviceStore 的 Redis 实现。
type deviceStore struct {
	cache redis.UniversalClient
	// ttl 是整个键的兜底有效期，0 表示不设置。
	//
	// 有序集合本应由登出 / 被踢显式清理，但客户端异常退出不会触发清理，
	// 长期运行会持续膨胀。给一个较长的 TTL（默认 30 天）后，
	// 长期不活跃的用户会被自然回收，而活跃用户每次登录都会续期。
	ttl time.Duration
}

// NewDeviceTTL 从配置读取设备在线标记的有效期。
//
// 必须导出：Wire 生成的装配代码位于 cmd 包的 main 包中。
func NewDeviceTTL(c *conf.Config) DeviceTTL {
	return DeviceTTL(c.App.OnlineDeviceTTL)
}

// NewDeviceStore 构造设备在线存储。
func NewDeviceStore(d *Data, ttl DeviceTTL) biz.DeviceStore {
	return &deviceStore{cache: d.cache, ttl: time.Duration(ttl)}
}

// Add 记录一台设备上线。
//
// score 用上线时间而非「当前时间」：由调用方传入 createAt，
// 使同一台设备重新登录时 score 被推进到新的登录时刻，从而排在后面
// （它确实是「最新加入」的那台，不应被优先淘汰）。
func (s *deviceStore) Add(ctx context.Context, userID int64, member string, at time.Time) error {
	key := deviceKey(userID)
	pipe := s.cache.Pipeline()
	pipe.ZAdd(ctx, key, redis.Z{
		Score:  float64(at.UnixMilli()),
		Member: member,
	})
	if s.ttl > 0 {
		// 每次写入都续期：只要用户在持续使用，这个键就不会过期
		pipe.Expire(ctx, key, s.ttl)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("data: 登记设备上线失败: %w", err)
	}
	return nil
}

// Oldest 返回上线时间最早的 n 台设备，按时间升序。
//
// 用 ZRange（升序）而不是 ZRevRange：淘汰规则是「踢出最先加入的」，
// 而「最先加入」正是 score 最小的一批。
func (s *deviceStore) Oldest(ctx context.Context, userID int64, n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}
	members, err := s.cache.ZRange(ctx, deviceKey(userID), 0, int64(n-1)).Result()
	if err != nil {
		return nil, fmt.Errorf("data: 查询最早上线的设备失败: %w", err)
	}
	return members, nil
}

// Keys 返回该用户当前记录的全部设备标识。
//
// ZCard 只给出数量、拿不到成员，因此这里用 ZRange 取全量：
// 淘汰逻辑需要「先知道总数、再取最早的一批」，两步都基于同一份数据，
// 比 ZCard + ZRange 两次调用更容易推理（不会因中间插入而错位）。
func (s *deviceStore) Keys(ctx context.Context, userID int64) ([]string, error) {
	members, err := s.cache.ZRange(ctx, deviceKey(userID), 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("data: 查询用户在线设备列表失败: %w", err)
	}
	return members, nil
}

// Remove 移除一台设备的上线标记。
func (s *deviceStore) Remove(ctx context.Context, userID int64, member string) error {
	if err := s.cache.ZRem(ctx, deviceKey(userID), member).Err(); err != nil {
		return fmt.Errorf("data: 移除设备在线标记失败: %w", err)
	}
	return nil
}

// Clear 移除该用户的全部设备标记。
func (s *deviceStore) Clear(ctx context.Context, userID int64) error {
	if err := s.cache.Del(ctx, deviceKey(userID)).Err(); err != nil {
		return fmt.Errorf("data: 清空设备在线标记失败: %w", err)
	}
	return nil
}
