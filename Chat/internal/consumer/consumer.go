// Package consumer 消费消息队列里的消息并推送给在线用户。
//
// 它是「消息已落库」到「用户看到消息」之间的最后一环。
// 与 relay 包的分工：
//   - relay 负责把 Outbox 事件搬到消息队列（生产侧）；
//   - consumer 负责从队列取出并把消息推给在线连接（消费侧）。
//
// 之所以拆成两个包而不是一个：两者的失败语义完全不同。
// 生产侧失败意味着消息可能丢失，必须重试到成功或进死信；
// 消费侧失败通常只是「用户此刻不在线」，重试没有意义——
// 离线消息由客户端按 seq 拉取补齐，那是另一条更可靠的路径。
package consumer

import (
	"context"
	"encoding/json"

	klog "github.com/go-kratos/kratos/v2/log"

	"github.com/Jesse-467/im/Chat/internal/biz"
	"github.com/Jesse-467/im/Chat/internal/mq"
	"github.com/Jesse-467/im/Chat/internal/ws"
)

// 消息推送参数
const (
	// memberBatchSize 是单次查询会话成员的批量上限。
	//
	// 群聊成员可能上千，一次全取会占用较多内存与网络；
	// 分批推送可以平滑负载，也让推送进度更可控。
	memberBatchSize = 500
)

// messageEvent 是投递事件的载荷。
//
// 与 biz.buildOutboxEvent 生成的格式严格对应。刻意设计成精简结构：
// 投递方只需知道「哪条会话的哪条消息」，完整消息由接收端按需求取，
// 这样后续给消息加字段不需要改动事件格式，避免新旧版本不兼容。
type messageEvent struct {
	ConversationID int64 `json:"conversationId"`
	MessageID      int64 `json:"messageId"`
	Seq            int64 `json:"seq"`
	SenderID       int64 `json:"senderId"`
	Type           int32 `json:"type"`
}

// Pusher 是下行推送能力。
type Pusher interface {
	PushToUser(userID int64, msg ws.Message)
	// IsOnlineLocally 判断用户在本节点是否有连接
	IsOnlineLocally(userID int64) bool
}

// EventLoader 按事件装载完整消息。
type EventLoader interface {
	LoadMessage(ctx context.Context, conversationID, seq int64) (*biz.Message, error)
}

// Consumer 消费消息并推送。
type Consumer struct {
	convRepo biz.ConversationRepo
	loader   EventLoader
	pusher   Pusher
	log      *klog.Helper
}

// New 构造消息消费者。
func New(
	convRepo biz.ConversationRepo,
	loader EventLoader,
	pusher Pusher,
	logger klog.Logger,
) *Consumer {
	return &Consumer{
		convRepo: convRepo,
		loader:   loader,
		pusher:   pusher,
		log:      klog.NewHelper(klog.With(logger, "module", "consumer")),
	}
}

// Handle 处理一条投递事件，签名与 mq.Handler 一致，可直接注册为订阅回调。
//
// 返回 nil 表示处理完毕（无论是否真的推送到人）。
// 刻意不把「用户不在线」当作错误：那是最常见的正常情况，
// 若将其认定为失败会让监控指标失去意义。
func (c *Consumer) Handle(ctx context.Context, topic, key string, value []byte) error {
	var ev messageEvent
	if err := json.Unmarshal(value, &ev); err != nil {
		// 载荷解析失败说明格式不兼容（如灰度期间的版本差异），
		// 重试也不会成功，直接丢弃并告警。
		c.log.Errorw("msg", "解析投递事件失败，丢弃", "topic", topic, "key", key, "err", err)
		return nil
	}

	if ev.ConversationID <= 0 || ev.Seq <= 0 {
		c.log.Warnw("msg", "投递事件缺少必要字段，丢弃", "payload", string(value))
		return nil
	}

	// 取出完整消息：事件里只有 ID 与 seq，推送需要完整的消息体
	// （内容、发送者、时间等）。按会话 + seq 精确查询，走唯一索引，开销可控。
	msg, err := c.loader.LoadMessage(ctx, ev.ConversationID, ev.Seq)
	if err != nil {
		c.log.Errorw("msg", "加载消息失败", "conversationId", ev.ConversationID, "seq", ev.Seq, "err", err)
		return err
	}
	if msg == nil {
		c.log.Warnw("msg", "消息不存在，可能已被清理", "conversationId", ev.ConversationID, "seq", ev.Seq)
		return nil
	}

	pushed, total := c.pushToMembers(ctx, msg)

	c.log.Infow("msg", "消息推送完成",
		"conversationId", msg.ConversationID,
		"messageId", msg.ID,
		"seq", msg.Seq,
		"members", total,
		"pushedConns", pushed,
	)
	return nil
}

// pushToMembers 把消息推送给会话的全部在线成员。
//
// 返回推送到的连接数与成员总数。
//
// 关键设计：只推给「本节点上有连接」的成员。
// 其他节点上的成员由那些节点各自的消费者负责推送——
// 这正是「每实例独立消费者组」的由来：每个实例都能收到全量消息，
// 各自负责自己那部分在线用户，无需实例间转发。
func (c *Consumer) pushToMembers(ctx context.Context, msg *biz.Message) (pushed, total int) {
	members, err := c.convRepo.ListMembers(ctx, msg.ConversationID)
	if err != nil {
		// 成员列表取不到时降级为只推发送者：至少让发送方拿到回执，
		// 其他成员可通过拉取补齐。
		c.log.Errorw("msg", "查询会话成员失败", "conversationId", msg.ConversationID, "err", err)
		if c.pusher.IsOnlineLocally(msg.SenderID) {
			c.pusher.PushToUser(msg.SenderID, c.buildPushMessage(msg))
			return 1, 1
		}
		return 0, 0
	}

	payload := c.buildPushMessage(msg)

	for i := 0; i < len(members); i += memberBatchSize {
		end := i + memberBatchSize
		if end > len(members) {
			end = len(members)
		}

		for _, m := range members[i:end] {
			total++
			// 跳过不在本节点的成员：避免无谓的推送尝试
			if !c.pusher.IsOnlineLocally(m.UserID) {
				continue
			}
			// 发送者也推送：多端登录时，发送方的其他端（如手机发了、网页也要看到）
			// 同样需要这条消息。发送方当前端会因 clientMsgId 去重，不会重复展示。
			c.pusher.PushToUser(m.UserID, payload)
			pushed++
		}
	}

	return pushed, total
}

// buildPushMessage 构造下行推送消息。
func (c *Consumer) buildPushMessage(msg *biz.Message) ws.Message {
	return ws.Message{
		Type: ws.TypeMessage,
		Data: pushPayload{
			ConversationID: msg.ConversationID,
			MessageID:      msg.ID,
			Seq:            msg.Seq,
			SenderID:       msg.SenderID,
			Type:           msg.Type,
			Content:        msg.Content,
			Extra:          msg.Extra,
			ClientMsgID:    msg.ClientMsgID,
			CreateTime:     msg.CreatedAt.UnixMilli(),
		},
	}
}

// pushPayload 是下行推送的消息体。
type pushPayload struct {
	ConversationID int64  `json:"conversationId"`
	MessageID      int64  `json:"messageId"`
	Seq            int64  `json:"seq"`
	SenderID       int64  `json:"senderId"`
	Type           int32  `json:"type"`
	Content        string `json:"content"`
	Extra          string `json:"extra,omitempty"`
	ClientMsgID    string `json:"clientMsgId"`
	CreateTime     int64  `json:"createTime"`
}

// 编译期断言：Consumer 可直接作为订阅回调使用。
var _ mq.Handler = (*Consumer)(nil).Handle
