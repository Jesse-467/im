package ws

import (
	"context"
	"fmt"
	"strconv"
	"time"

	klog "github.com/go-kratos/kratos/v2/log"
	"github.com/redis/go-redis/v9"
)

// 在线状态参数
const (
	// routeKeyPrefix 是「用户在线路由」的 Redis 键前缀。
	//
	// 结构为 Hash：field = nodeID，value = 该节点最后一次心跳的 Unix 毫秒时间戳。
	//
	// 为什么用「节点 + 心跳时间戳」而不是「用户ID集合」：
	//   - 多节点部署时同一用户可能被不同节点持有（重连漂移），
	//     记录时间戳可以判断哪个节点仍然存活，从而只投给活着的节点；
	//   - 若只存节点列表，节点异常退出后残留的脏数据会导致消息投递给
	//     已经不存在的节点，表现为「消息发出但对方收不到」。
	routeKeyPrefix = "im:route:"

	// nodeTTL 是单个节点心跳的有效期。
	//
	// 取值需大于心跳间隔的数倍：网络抖动导致一两次心跳丢失不应让用户离线，
	// 但取值过大又会让宕机节点长时间残留。这里取 90 秒，
	// 配合 30 秒的节点心跳，可容忍连续 2 次心跳丢失。
	nodeTTL = 90 * time.Second

	// nodeHeartbeatInterval 是节点心跳间隔
	nodeHeartbeatInterval = 30 * time.Second
)

// Presence 负责在线状态的登记与跨节点查询。
//
// 与 Registry 的分工：
//   - Registry 是进程内的连接表，回答「这个用户的连接在本节点吗」；
//   - Presence 是跨节点的路由表，回答「这个用户在哪个节点上」。
//
// 投递时先查 Presence 定位节点，若就是本节点则走 Registry 直接投递，
// 否则把消息转发给对应节点。
type Presence struct {
	cache  redis.UniversalClient
	nodeID string
	log    *klog.Helper
}

// NewPresence 构造在线状态管理器。
func NewPresence(cache redis.UniversalClient, nodeID NodeName, logger klog.Logger) *Presence {
	return &Presence{
		cache:  cache,
		nodeID: string(nodeID),
		log:    klog.NewHelper(klog.With(logger, "module", "ws/presence")),
	}
}

// NodeID 返回本节点标识。
func (p *Presence) NodeID() string { return p.nodeID }

// online 标记用户在本节点上线。
//
// 采用「先写入再设 TTL」而非单条 SETEX：Hash 中记录的是节点维度的心跳，
// 多个节点可以同时登记同一用户（用户在不同节点各有一条连接，
// 例如重连时新旧连接短暂并存），因此不能覆盖整键。
func (p *Presence) online(ctx context.Context, userID int64) error {
	key := routeKey(userID)
	now := time.Now().UnixMilli()

	pipe := p.cache.Pipeline()
	pipe.HSet(ctx, key, p.nodeID, now)
	// TTL 设在键上而非 field 上：Redis 的 Hash 不支持 field 级 TTL。
	// 因此键的存活时间由最后一次写入刷新，这也意味着只要有任一节点
	// 保持心跳，整个键就不会过期——过期的节点由查询时的存活判断过滤。
	pipe.Expire(ctx, key, nodeTTL*2)
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("ws: 登记在线状态失败: %w", err)
	}
	return nil
}

// offline 标记用户在本节点下线。
func (p *Presence) offline(ctx context.Context, userID int64) error {
	key := routeKey(userID)

	// 只删本节点这一条 field，而不是删整个键：
	// 同一用户可能在其他节点仍有连接，删键会让那些连接"被离线"。
	if err := p.cache.HDel(ctx, key, p.nodeID).Err(); err != nil {
		return fmt.Errorf("ws: 清除在线状态失败: %w", err)
	}

	// 若已无任何节点持有该用户，把键一并删掉，避免残留空 Hash 占用内存
	n, err := p.cache.HLen(ctx, key).Result()
	if err == nil && n == 0 {
		p.cache.Del(ctx, key)
	}
	return nil
}

// Nodes 返回用户当前所在的活跃节点列表。
//
// 判断标准是「心跳时间戳在有效期之内」：超时的节点视为已宕机，
// 会被顺带清理掉（惰性清理），避免脏数据长期堆积。
func (p *Presence) Nodes(ctx context.Context, userID int64) ([]string, error) {
	key := routeKey(userID)

	entries, err := p.cache.HGetAll(ctx, key).Result()
	if err != nil {
		// 读不到路由信息时返回空列表而非错误：调用方会退化为
		// 「本地投递 + 依赖客户端拉取」，比让整条投递链路失败更好。
		return nil, fmt.Errorf("ws: 查询在线路由失败: %w", err)
	}
	if len(entries) == 0 {
		return nil, nil
	}

	deadline := time.Now().Add(-nodeTTL).UnixMilli()
	nodes := make([]string, 0, len(entries))
	stale := make([]string, 0)

	for node, raw := range entries {
		ts, perr := strconv.ParseInt(raw, 10, 64)
		if perr != nil || ts < deadline {
			stale = append(stale, node)
			continue
		}
		nodes = append(nodes, node)
	}

	// 惰性清理超时节点：只在查询时顺手做，不需要独立的后台清理任务
	if len(stale) > 0 {
		fields := make([]string, 0, len(stale))
		for _, s := range stale {
			fields = append(fields, s)
		}
		p.cache.HDel(ctx, key, fields...)
		p.log.Infow("msg", "已清理超时的在线路由", "uid", userID, "nodes", fields)
	}

	return nodes, nil
}

// IsOnline 判断用户是否在线（任意节点）。
func (p *Presence) IsOnline(ctx context.Context, userID int64) (bool, error) {
	nodes, err := p.Nodes(ctx, userID)
	if err != nil {
		return false, err
	}
	return len(nodes) > 0, nil
}

// Heartbeat 刷新本节点在该用户上的心跳。
//
// 由连接的心跳循环调用。之所以要按用户刷新而不是只标记节点存活：
// 路由表是「用户 → 节点」的映射，必须逐个用户续期才能反映
// 「该用户仍连接在这个节点上」这一事实。
func (p *Presence) Heartbeat(ctx context.Context, userID int64) error {
	return p.online(ctx, userID)
}

// Online 登记上线。
func (p *Presence) Online(ctx context.Context, userID int64) error {
	return p.online(ctx, userID)
}

// Offline 登记下线。
func (p *Presence) Offline(ctx context.Context, userID int64) error {
	return p.offline(ctx, userID)
}

// routeKey 生成用户的路由键。
func routeKey(userID int64) string {
	return routeKeyPrefix + strconv.FormatInt(userID, 10)
}

// 供测试与监控使用
var _ = nodeHeartbeatInterval
