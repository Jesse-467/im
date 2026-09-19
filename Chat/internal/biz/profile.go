package biz

import "context"

// UserBrief 是会话展示所需的用户最小信息集。
type UserBrief struct {
	UserID    int64
	Nickname  string
	AvatarURL string
}

// UserProvider 提供用户基础信息。
//
// 聊天服务刻意不持有用户资料：昵称、头像的唯一权威来源是账号中心。
// 如果把用户资料冗余到聊天库，就要处理跨服务双写与数据漂移；
// 这里改为按需实时读取（实现方需自行缓存，避免放大跨服务调用量）。
//
// 定义成接口而非直接依赖 gRPC 客户端，是为了让 biz 层保持框架无关，
// 单测时可以替换为内存实现。
type UserProvider interface {
	// BatchGetBriefs 批量获取用户简要信息，返回 userID -> brief 的映射。
	// 不存在的用户不会出现在返回值中，调用方需自行兜底。
	BatchGetBriefs(ctx context.Context, userIDs []int64) (map[int64]*UserBrief, error)
}

// TokenVerifier 向账号中心确认令牌是否仍然有效。
//
// 为什么聊天的鉴权必须回到账号中心：
// 聊天的 HTTP 中间件与 WS 网关原本只用本地 auth.Parse 做「签名 + 过期」检查
// （等价于 Account 的 self 模式）。这在单模式下没有问题，但一旦账号中心
// 开启了 db 模式并通过「多设备在线上限」吊销令牌，聊天侧就会继续放行
// 已被吊销的令牌——表现为「设备已被踢下线，却仍能收发消息」。
//
// 因此这里把「令牌是否被吊销」的判断权交回账号中心（它是令牌的唯一权威），
// 聊天侧只负责转发结论。
type TokenVerifier interface {
	// VerifyToken 校验令牌。返回的 valid=false 表示令牌不该被接受
	// （过期、被吊销、签名错），属于正常业务分支；
	// 只有网络/服务端故障才返回 error，此时调用方应按 fail-closed 拒绝。
	VerifyToken(ctx context.Context, accessToken string) (valid bool, userID int64, err error)
}

// DisplayName 返回用户展示名，取不到昵称时用 ID 兜底，避免前端出现空白项。
func (b *UserBrief) DisplayName() string {
	if b == nil || b.Nickname == "" {
		return ""
	}
	return b.Nickname
}
