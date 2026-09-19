package data

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Jesse-467/im/Chat/internal/biz"
)

// 编译期断言：发号器必须满足业务层声明的接口。
var _ biz.SeqAllocator = (*seqAllocator)(nil)

// seqKeyPrefix 是序号键前缀。
// 统一带业务前缀，便于在同一 Redis 实例中与其他业务隔离，也便于按前缀监控与清理。
const seqKeyPrefix = "im:seq:"

// seqKeyTTL 是序号键的过期时间。
//
// 敢于设置 TTL 的前提是序号可以回填：即使键过期，也能从 message 表的
// MAX(seq) 恢复，不会出现「序号从头开始」从而撞上唯一约束。
const seqKeyTTL = 7 * 24 * time.Hour

// seedScript 是「不存在则以数据库最大值为种子初始化」的 Lua 脚本。
//
// 为什么必须用脚本而不是「GET → 判断 → SETNX」：
// 后者在多实例并发时会同时判定键不存在，各自设置种子并返回同一个序号，
// 最终触发唯一约束。Lua 在 Redis 中原子执行，能彻底避免这一竞态。
//
// KEYS[1] 序号键
// ARGV[1] 种子值（数据库中的历史最大值）
// ARGV[2] TTL 秒
const seedScript = `
if redis.call('EXISTS', KEYS[1]) == 0 then
  redis.call('SET', KEYS[1], ARGV[1], 'EX', ARGV[2])
end
return redis.call('INCR', KEYS[1])
`

// seqAllocator 基于 Redis 实现序号分配。
//
// 为什么用 Redis 而不是数据库：
//   - Redis 的 INCR 是原子的，无行锁竞争，延迟稳定；
//   - 数据库方案（自增列或计数表）在「每条消息都要发号」这条高频写路径上
//     会因行锁成为瓶颈。
//
// 两个序号维度：
//   - 会话序号：会话内全局递增，是消息顺序的权威依据；
//   - 发送者序号：会话内单个发送者递增，用于把「同一发送者的插入」锁成唯一，
//     使客户端换 client_msg_id 重试也无法绕过幂等。
type seqAllocator struct {
	data *Data

	// seeds 缓存每个键已知的数据库水位，避免每次自增都去查库。
	//
	// 只在 Redis 键首次创建时才需要查库播种，之后一直走 Redis。
	// 用 map 缓存是为了应对「键被淘汰后同一会话再次发号」的重复查询。
	mu    sync.RWMutex
	seeds map[string]int64
}

// NewSeqAllocator 构造序号分配器。
func NewSeqAllocator(d *Data) biz.SeqAllocator {
	return &seqAllocator{
		data:  d,
		seeds: make(map[string]int64),
	}
}

// Next 分配会话内全局序号。
func (a *seqAllocator) Next(ctx context.Context, conversationID int64) (int64, error) {
	return a.alloc(ctx, seqKeyPrefix+"c:"+strconv.FormatInt(conversationID, 10), conversationID, 0)
}

// NextSenderSeq 分配「会话内某发送者」的序号。
func (a *seqAllocator) NextSenderSeq(ctx context.Context, conversationID, senderID int64) (int64, error) {
	key := seqKeyPrefix + "s:" + strconv.FormatInt(conversationID, 10) + ":" + strconv.FormatInt(senderID, 10)
	return a.alloc(ctx, key, conversationID, senderID)
}

// alloc 执行一次分配。
func (a *seqAllocator) alloc(ctx context.Context, key string, conversationID, senderID int64) (int64, error) {
	seed := a.knownSeed(key)
	if seed == 0 {
		// 首次见到该键（或缓存被清理）：查一次数据库拿历史水位。
		// 查不到说明是全新会话，种子为 0，脚本会从 1 开始发号。
		var err error
		seed, err = a.queryMaxSeq(ctx, conversationID, senderID)
		if err != nil {
			// 查库失败不阻断：用 0 作为种子继续尝试。
			// 若数据库确有历史数据，唯一约束会拦下冲突，上层按幂等处理，
			// 好于直接让发消息失败。
			seed = 0
		}
		a.rememberSeed(key, seed)
	}

	seq, err := a.data.cache.Eval(ctx, seedScript, []string{key}, seed, int(seqKeyTTL.Seconds())).Int64()
	if err != nil {
		return 0, fmt.Errorf("data: 分配序号失败: %w", err)
	}
	return seq, nil
}

// queryMaxSeq 查数据库中已有的最大序号，作为 Redis 键的种子。
//
// senderID > 0 时查该发送者在会话内的最大发送者序号，否则查会话全局最大序号。
func (a *seqAllocator) queryMaxSeq(ctx context.Context, conversationID, senderID int64) (int64, error) {
	var maxSeq *int64

	if senderID > 0 {
		err := a.data.conn(ctx).
			Model(&messageModel{}).
			Select("MAX(sender_seq)").
			Where("conversation_id = ? AND sender_id = ?", conversationID, senderID).
			Scan(&maxSeq).Error
		if err != nil {
			return 0, err
		}
	} else {
		err := a.data.conn(ctx).
			Model(&messageModel{}).
			Select("MAX(seq)").
			Where("conversation_id = ?", conversationID).
			Scan(&maxSeq).Error
		if err != nil {
			return 0, err
		}
	}

	if maxSeq == nil || *maxSeq < 0 {
		return 0, nil
	}
	return *maxSeq, nil
}

// knownSeed 读取缓存的种子。
func (a *seqAllocator) knownSeed(key string) int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.seeds[key]
}

// rememberSeed 记录种子。
//
// 只在种子大于已知值时更新：并发下可能有多个请求同时查库，
// 取较大值可以确保不会因后到的较小值把水位压低。
func (a *seqAllocator) rememberSeed(key string, seed int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if seed > a.seeds[key] {
		a.seeds[key] = seed
	}
}

// 用于触发 redis 包的类型引用，避免未使用导入。
var _ = redis.Nil
