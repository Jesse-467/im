package data

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	klog "github.com/go-kratos/kratos/v2/log"
	kratosgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
	"github.com/redis/go-redis/v9"

	accountv1 "github.com/Jesse-467/im/pkg/account/v1"

	"github.com/Jesse-467/im/Chat/internal/biz"
	"github.com/Jesse-467/im/Chat/internal/conf"
)

// 编译期断言：账号中心客户端必须满足业务层声明的接口。
var (
	_ biz.UserProvider  = (*accountClient)(nil)
	_ biz.TokenVerifier = (*accountClient)(nil)
)

// userBriefCacheTTL 是用户简要信息的缓存时长。
//
// 取 10 分钟是在「跨服务调用量」与「资料变更可见延迟」之间的折中：
// 昵称、头像的实时性要求不高，而会话列表每次刷新都要用到它们，
// 缓存能把账号中心的读压力从「每次刷新 × 会话数」压到「每人每 10 分钟一次」。
const userBriefCacheTTL = 10 * time.Minute

// userBriefCacheKey 拼接用户简要信息的缓存键。
//
// 统一前缀便于运维按前缀扫描/清理，也避免与账号中心自身可能使用的键空间冲突。
func userBriefCacheKey(uid int64) string {
	return fmt.Sprintf("im:user:brief:%d", uid)
}

// accountClient 通过 gRPC 消费账号中心的用户资料能力，并在本地做一层缓存。
//
// 聊天服务刻意不存储用户资料（唯一权威在账号中心），因此这里只做「读 + 缓存」：
// 缓存不是为了省事，而是因为会话列表是最高频接口，逐次跨服务调用会成倍放大
// 账号中心的压力。
type accountClient struct {
	cli   accountv1.AccountServiceClient
	cache redis.UniversalClient
	log   *klog.Helper
}

// NewAccountClient 建立到账号中心的 gRPC 连接。
//
// 复用 Data 持有的缓存客户端：缓存连接池应按进程共享，若这里另建一个客户端，
// 会多占一份连接与内存，还会让清理逻辑分散在两处。
//
// gRPC 连接本身自带重连与负载均衡，这里不做额外的健康探测或主动重连；
// kratos 的 DialInsecure 在地址暂时不可达时也不会立刻失败，
// 因此账号中心晚于聊天服务启动不会导致本服务无法启动。
func NewAccountClient(d *Data, c *conf.Config, logger klog.Logger) (biz.UserProvider, func(), error) {
	conn, err := kratosgrpc.DialInsecure(context.Background(),
		kratosgrpc.WithEndpoint(c.App.AccountRPCEndpoint),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("data: 连接账号中心失败 (%s): %w", c.App.AccountRPCEndpoint, err)
	}

	log := klog.NewHelper(klog.With(logger, "module", "data/account"))

	cleanup := func() {
		if err := conn.Close(); err != nil {
			log.Warnw("msg", "关闭账号中心连接出错", "err", err)
		}
	}

	return &accountClient{
		cli:   accountv1.NewAccountServiceClient(conn),
		cache: d.Cache(),
		log:   log,
	}, cleanup, nil
}

// VerifyToken 向账号中心确认令牌是否仍然有效。
//
// 这是「设备被踢下线 / 改密」能即时生效的关键链路：聊天的本地校验只能判断
// 签名与过期时间，无法知道令牌是否已被吊销，因此必须回到账号中心确认。
//
// 返回值语义严格对齐账号中心的约定：
//   - (false, 0, nil)  → 令牌不该被接受（过期 / 被吊销 / 签名错），业务分支；
//   - (false, 0, err)  → 调用失败（网络或账号中心故障），调用方应 fail-closed。
//
// 刻意不在此处缓存结果：缓存的窗口期内被吊销的令牌仍会被放行，
// 这与本功能的目的（即时踢下线）相冲突。
func (c *accountClient) VerifyToken(ctx context.Context, accessToken string) (bool, int64, error) {
	resp, err := c.cli.VerifyToken(ctx, &accountv1.VerifyTokenRequest{AccessToken: accessToken})
	if err != nil {
		// 如实返回错误而不是 (false, nil)：账号中心不可达属于服务端故障，
		// 调用方需要区分「令牌无效」与「无法确认」，否则监控上看不出来。
		return false, 0, fmt.Errorf("data: 调用账号中心校验令牌失败: %w", err)
	}
	if !resp.GetValid() {
		return false, 0, nil
	}
	return true, resp.GetUserId(), nil
}

// BatchGetBriefs 批量获取用户简要信息，命中缓存的用户不再访问账号中心。
//
// 入参先去重，再对未命中的部分发起一次批量调用：调用量只与「缓存未命中的人数」
// 相关，与请求总量无关，避免逐个用户循环调用把下游压垮。
func (c *accountClient) BatchGetBriefs(ctx context.Context, userIDs []int64) (map[int64]*biz.UserBrief, error) {
	briefs := make(map[int64]*biz.UserBrief, len(userIDs))

	ids := dedupUserIDs(userIDs)
	if len(ids) == 0 {
		return briefs, nil
	}

	missed := c.loadCached(ctx, ids, briefs)
	if len(missed) == 0 {
		return briefs, nil
	}

	resp, err := c.cli.BatchGetUsers(ctx, &accountv1.BatchGetUsersRequest{UserIds: missed})
	if err != nil {
		// 用户资料是可降级能力：调用方拿到错误后会用空资料兜底展示，
		// 因此这里如实返回错误，便于监控发现账号中心异常，而不是静默返回半份数据。
		return nil, fmt.Errorf("data: 调用账号中心批量查询用户失败: %w", err)
	}

	profiles := resp.GetProfiles()
	fresh := make(map[int64]*biz.UserBrief, len(profiles))
	for uid, profile := range profiles {
		if profile == nil {
			continue
		}
		// 以请求的 uid 为键（服务端返回的 map 键即 uid），缺失的用户不写入返回值，
		// 由调用方按「取不到资料」兜底，避免在上游产生空资料占位。
		fresh[uid] = &biz.UserBrief{
			UserID:    uid,
			Nickname:  profile.GetNickname(),
			AvatarURL: profile.GetAvatarUrl(),
		}
	}

	for uid, brief := range fresh {
		briefs[uid] = brief
	}
	c.storeCached(ctx, fresh)
	return briefs, nil
}

// loadCached 从缓存读取用户资料，返回缓存未命中的用户 ID。
//
// 缓存读取失败只记录日志并整体降级为「全部未命中」：缓存是加速手段而非数据源，
// 不能让它成为会话列表的单点（Redis 抖动不应导致聊天不可用）。
func (c *accountClient) loadCached(ctx context.Context, ids []int64, out map[int64]*biz.UserBrief) []int64 {
	keys := make([]string, 0, len(ids))
	for _, uid := range ids {
		keys = append(keys, userBriefCacheKey(uid))
	}

	values, err := c.cache.MGet(ctx, keys...).Result()
	if err != nil {
		c.log.Errorw("msg", "读取用户资料缓存失败，降级为直连账号中心", "err", err, "count", len(ids))
		return ids
	}

	missed := make([]int64, 0, len(ids))
	// MGet 返回顺序与 keys 一致，因此可以按位次还原对应的用户 ID
	for i, value := range values {
		if i >= len(ids) {
			break
		}
		raw, ok := value.(string)
		if !ok || raw == "" {
			missed = append(missed, ids[i])
			continue
		}

		var brief biz.UserBrief
		if err := json.Unmarshal([]byte(raw), &brief); err != nil || brief.UserID <= 0 {
			// 缓存内容损坏时按未命中处理，让这次请求通过 RPC 拿到正确数据并覆盖缓存
			missed = append(missed, ids[i])
			continue
		}
		out[brief.UserID] = &brief
	}
	return missed
}

// storeCached 回写缓存。写入失败只告警：本次请求已经拿到数据，
// 不应因为缓存不可用而对外报错。
func (c *accountClient) storeCached(ctx context.Context, briefs map[int64]*biz.UserBrief) {
	if len(briefs) == 0 {
		return
	}

	pipe := c.cache.Pipeline()
	for uid, brief := range briefs {
		payload, err := json.Marshal(brief)
		if err != nil {
			continue
		}
		pipe.Set(ctx, userBriefCacheKey(uid), payload, userBriefCacheTTL)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		c.log.Errorw("msg", "写入用户资料缓存失败", "err", err, "count", len(briefs))
	}
}

// dedupUserIDs 去掉重复与非法 ID，保持首次出现的顺序。
//
// 会话列表中同一个人可能出现在多个会话（单聊、群聊），去重能显著减少批量查询的入参，
// 也避免 MGet 的键数量被无意义地放大。
func dedupUserIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, uid := range ids {
		if uid <= 0 {
			continue
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		out = append(out, uid)
	}
	return out
}
