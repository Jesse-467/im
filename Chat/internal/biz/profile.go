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
// 这里改为按需实时读取（实现方需自行加缓存，避免放大跨服务调用量）。
//
// 定义成接口而非直接依赖 gRPC 客户端，是为了让 biz 层保持框架无关，
// 单测时可以替换为内存实现。
type UserProvider interface {
	// BatchGetBriefs 批量获取用户简要信息，返回 userID -> brief 的映射。
	// 不存在的用户不会出现在返回值中，调用方需自行兜底。
	BatchGetBriefs(ctx context.Context, userIDs []int64) (map[int64]*UserBrief, error)
}

// DisplayName 返回用户展示名，取不到昵称时用 ID 兜底，避免前端出现空白项。
func (b *UserBrief) DisplayName() string {
	if b == nil || b.Nickname == "" {
		return ""
	}
	return b.Nickname
}
