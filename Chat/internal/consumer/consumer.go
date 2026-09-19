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

	// dedup 记录本节点最近推送过的 (会话, 序号)。
	//
	// 这是「不重复推送」的第二道防线。第一道在 Outbox：捞取即置为投递中，
	// 从源头避免同一事件被投递两次。但仍有两条路径会产生重复：
	//   - 投递成功后确认失败（进程崩溃），事件被回收重投；
	//   - Kafka 的 at-least-once 语义本身允许重复。
	//
	// 因此消费端必须幂等。用内存而非 Redis：重复投递几乎总是落在同一节点
	// （同一分区由同一消费者处理），加一次跨网络查询不值当。
	dedup *dedupCache
}

// dedupCapacity 是去重窗口的大小。
//
// 取值权衡：窗口太小会让间隔较久的重复投递漏过；太大则占用内存。
// 这里按「单节点近期活跃会话数 × 每会话少量消息」估算，
// 覆盖分钟级的重复投递窗口已经足够——更久的重复本就会被客户端
// 按 (conversationId, seq) 去重。
const dedupCapacity = 100_000

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
		dedup:    newDedupCache(dedupCapacity),
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

	// 幂等闸门：同一 (会话, 序号) 只推送一次。
	//
	// 放在加载消息之前：重复投递时连查询都能省掉。
	// 注意必须在这条消息真正处理完之后才记录——若先记录后推送、
	// 而推送中途失败，这条消息就再也不会被重试了，等于人为制造丢消息。
	if !c.dedup.enter(msgKey(ev.ConversationID, ev.Seq)) {
		c.log.Infow("msg", "投递事件重复，已跳过",
			"conversationId", ev.ConversationID, "seq", ev.Seq)
		return nil
	}

	// 取出完整消息：事件里只有 ID 与 seq，推送需要完整的消息体
	// （内容、发送者、时间等）。按会话 + seq 精确查询，走唯一索引，开销可控。
	msg, err := c.loader.LoadMessage(ctx, ev.ConversationID, ev.Seq)
	if err != nil {
		// 加载失败要放行去重键，否则这次失败会让该消息永远无法重推
		c.dedup.leave(msgKey(ev.ConversationID, ev.Seq))
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
			ConversationID: ws.ID(msg.ConversationID),
			MessageID:      ws.ID(msg.ID),
			Seq:            msg.Seq,
			SenderID:       ws.ID(msg.SenderID),
			Type:           msg.Type,
			Content:        msg.Content,
			Extra:          msg.Extra,
			ClientMsgID:    msg.ClientMsgID,
			CreateTime:     msg.CreatedAt.UnixMilli(),
		},
	}
}

// pushPayload 是下行推送的消息体。
//
// ID 类字段统一序列化为 JSON 字符串：会话 ID 与消息 ID 是 19 位雪花值，
// 超出 JavaScript 的 Number.MAX_SAFE_INTEGER，浏览器用 JSON.parse 读会被
// 静默舍入成另一个数，客户端再回传就会指向不存在的会话。
// 这里用 ID 类型（见 ws.ID）保证与 HTTP 接口的表示完全一致，
// 避免同一个 ID 在两条链路上形态不同而让客户端要做两套处理。
type pushPayload struct {
	ConversationID ws.ID  `json:"conversationId"`
	MessageID      ws.ID  `json:"messageId"`
	Seq            int64  `json:"seq"`
	SenderID       ws.ID  `json:"senderId"`
	Type           int32  `json:"type"`
	Content        string `json:"content"`
	Extra          string `json:"extra,omitempty"`
	ClientMsgID    string `json:"clientMsgId"`
	CreateTime     int64  `json:"createTime"`
}

// 编译期断言：Consumer 可直接作为订阅回调使用。
var _ mq.Handler = (*Consumer)(nil).Handle
