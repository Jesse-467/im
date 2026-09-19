package service

import (
	"context"

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

	// 只负责「落库」，不在此处回推给发送者。
	//
	// 回推由消息消费者统一完成（它会给会话全部成员含发送者推送完整消息体）。
	// 若这里也推一条回执，发送者会在同一毫秒内收到两条内容不同但指向同一
	// 消息的推送（一条是精简回执、一条是完整消息），客户端必须自行去重——
	// 这是把「确认」与「广播」拆开造成的冗余。
	//
	// 代价是发送者要等一次 Outbox 投递往返才能确认，
	// 换来的是客户端逻辑简单且不会重复渲染。
	if _, err := s.msgUC.Send(ctx, &biz.SendMessageRequest{
		ConversationID: convID,
		SenderID:       userID,
		Type:           msg.MsgType,
		Content:        msg.Content,
		Extra:          msg.Extra,
		ClientMsgID:    msg.ClientMsgID,
	}); err != nil {
		return toErrs(err)
	}

	return nil
}

// resolveConvID 解析会话标识，兼容数字 ID 与历史字符串形式。
func (s *ChatService) resolveConvID(ctx context.Context, conversationID int64, groupID string) (int64, error) {
	if conversationID > 0 {
		return conversationID, nil
	}
	return s.convUC.ResolveConversationID(ctx, groupID)
}
