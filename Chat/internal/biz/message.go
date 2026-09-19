package biz

import (
	"context"
	"time"
)

// 消息类型
const (
	MessageTypeText   int32 = 1
	MessageTypeImage  int32 = 2
	MessageTypeVideo  int32 = 3
	MessageTypeAudio  int32 = 4
	MessageTypeSystem int32 = 5
)

// 消息状态
const (
	MessageStatusNormal   int32 = 1
	MessageStatusRecalled int32 = 2
	MessageStatusDeleted  int32 = 3
)

// Message 是消息实体。
type Message struct {
	ID             int64
	ConversationID int64
	// Seq 是会话内单调递增的序号，是消息顺序的权威依据。
	// 之所以不用自增主键：分表后主键不再全局有序，且跨会话比较没有意义。
	Seq      int64
	SenderID int64
	Type     int32
	Content  string
	// Extra 承载富媒体元信息等扩展内容，以 JSON 字符串原样透传
	Extra string
	// ClientMsgID 是客户端幂等键，重试发送不会产生重复消息
	ClientMsgID string
	Status      int32
	CreatedAt   time.Time
}

// MessageRepo 是消息仓储。
//
// 当前阶段只暴露会话列表所需的"取最后一条消息"能力；完整的收发、
// 按 seq 拉取与离线同步会在消息模块中扩展本接口。
type MessageRepo interface {
	// FindLastByConversations 批量取每个会话的最后一条消息。
	// 必须一次性查询，避免会话列表出现 N+1。
	FindLastByConversations(ctx context.Context, conversationIDs []int64) (map[int64]*Message, error)
}
