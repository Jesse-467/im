package service

import (
	"context"
	"encoding/json"

	"github.com/Jesse-467/im/Chat/internal/biz"
	"github.com/Jesse-467/im/Chat/internal/ws"
)

// 上行消息类型
const (
	upstreamTypeSend = "send"
)

// HandleUpstream 处理客户端经 WebSocket 上行的消息。
//
// 这是 WS 与业务层的衔接点：网关负责连接与协议，业务规则全部复用
// MessageUseCase，因此「经 WS 发送」与「经 HTTP 发送」的校验、幂等、
// 发号、Outbox 行为完全一致，不会出现两套语义。
func (s *ChatService) HandleUpstream(ctx context.Context, userID int64, msg *ws.UpstreamMessage) error {
	switch msg.Type {
	case upstreamTypeSend:
		return s.handleWSSend(ctx, userID, msg)
	default:
		// 未知类型不报错：客户端版本可能比服务端新，
		// 直接忽略比返回错误更利于灰度升级。
		s.log.Infow("msg", "忽略未知的上行消息类型", "uid", userID, "type", msg.Type)
		return nil
	}
}

// handleWSSend 处理经 WebSocket 发送消息。
func (s *ChatService) handleWSSend(ctx context.Context, userID int64, msg *ws.UpstreamMessage) error {
	convID, err := s.resolveConvID(ctx, msg.ConversationID, msg.GroupID)
	if err != nil {
		return toErrs(err)
	}

	res, err := s.msgUC.Send(ctx, &biz.SendMessageRequest{
		ConversationID: convID,
		SenderID:       userID,
		Type:           msg.MsgType,
		Content:        msg.Content,
		Extra:          msg.Extra,
		ClientMsgID:    msg.ClientMsgID,
	})
	if err != nil {
		return toErrs(err)
	}

	s.pushSendAck(userID, convID, res)
	return nil
}

// resolveConvID 解析会话标识，兼容数字 ID 与历史字符串形式。
func (s *ChatService) resolveConvID(ctx context.Context, conversationID int64, groupID string) (int64, error) {
	if conversationID > 0 {
		return conversationID, nil
	}
	return s.convUC.ResolveConversationID(ctx, groupID)
}

// pushSendAck 把发送结果回推给发送者本人。
//
// 为什么要回推：客户端据此确认消息已落库，并拿到服务端分配的
// 消息 ID 与 seq 写入本地。若省略这一步，发送方要等下一次拉取
// 才能知道自己的消息序号，表现为「自己发的消息排序位置不确定」。
//
// 刻意只推给发送者本人：其他成员的推送由消息消费者完成，
// 这里若一并推送会导致发送方所在会话的其他成员收到重复消息。
func (s *ChatService) pushSendAck(userID, convID int64, res *biz.SendResult) {
	if s.pusher == nil || res == nil || res.Message == nil {
		return
	}

	s.pusher.PushToUser(userID, ws.Message{
		Type: ws.TypeMessage,
		Data: sendAckPayload{
			ConversationID: convID,
			MessageID:      res.Message.ID,
			Seq:            res.Message.Seq,
			ClientMsgID:    res.Message.ClientMsgID,
			Duplicated:     res.Duplicated,
			CreateTime:     res.Message.CreatedAt.UnixMilli(),
		},
	})
}

// sendAckPayload 是发送回执的下行载荷。
type sendAckPayload struct {
	ConversationID int64  `json:"conversationId"`
	MessageID      int64  `json:"messageId"`
	Seq            int64  `json:"seq"`
	ClientMsgID    string `json:"clientMsgId"`
	Duplicated     bool   `json:"duplicated"`
	CreateTime     int64  `json:"createTime"`
}

// Pusher 是下行推送能力。
//
// 定义成接口而非直接依赖 ws.Registry：service 包因此不需要知道
// 连接管理的实现细节，单测时可以替换为记录型假实现来断言推送内容。
type Pusher interface {
	// PushToUser 向指定用户在本节点的全部连接推送消息
	PushToUser(userID int64, msg ws.Message)
}

// 用编译期断言确保 json 包被使用（载荷序列化在 ws 层完成）
var _ = json.Marshal
