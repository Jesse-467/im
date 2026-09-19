package data

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/Jesse-467/im/Chat/internal/biz"
)

// 编译期断言：仓储实现必须满足业务层声明的接口。
var (
	_ biz.MessageRepo = (*messageRepo)(nil)
	_ biz.OutboxRepo  = (*outboxRepo)(nil)
)

// messageRepo 是 biz.MessageRepo 的 GORM 实现。
type messageRepo struct{ data *Data }

// NewMessageRepo 构造消息仓储。
func NewMessageRepo(d *Data) biz.MessageRepo { return &messageRepo{data: d} }

// Create 在单个事务内写入消息与对应的 Outbox 事件。
//
// 事务是这里的核心：消息与事件必须共存亡。若分两次写，
// 进程在「消息已写入、事件未写入」之间崩溃时，这条消息将永远不会被推送，
// 而服务端却认为一切正常——这是最难发现的一类消息丢失。
func (r *messageRepo) Create(ctx context.Context, msg *biz.Message, event *biz.OutboxEvent) error {
	return r.data.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(fromBizMessage(msg)).Error; err != nil {
			// 唯一约束冲突交由上层按幂等语义处理，这里不吞掉错误
			return fmt.Errorf("data: 写入消息失败: %w", err)
		}

		// 会话序号高水位用 CAS 推进：只在更大时更新，保证并发下不回退。
		// 同时刷新 updated_at，让会话列表能按「最近活跃」排序。
		if err := tx.Model(&conversationModel{}).
			Where("id = ? AND max_seq < ?", msg.ConversationID, msg.Seq).
			Updates(map[string]any{
				"max_seq":    msg.Seq,
				"updated_at": time.Now(),
			}).Error; err != nil {
			return fmt.Errorf("data: 推进会话序号失败: %w", err)
		}

		// 未读数按成员累加：发送者自己的未读不增加，其余成员 +1。
		// 放在同一事务里，避免出现「有新消息但红点没亮」。
		if err := tx.Model(&conversationMemberModel{}).
			Where("conversation_id = ? AND user_id <> ? AND left_at IS NULL", msg.ConversationID, msg.SenderID).
			Update("unread_count", gorm.Expr("unread_count + 1")).Error; err != nil {
			return fmt.Errorf("data: 更新未读数失败: %w", err)
		}

		// 事件写入同一个事务：这是 Outbox 模式的关键一步
		return tx.Create(&messageOutboxModel{
			EventID:      event.EventID,
			Topic:        event.Topic,
			PartitionKey: event.PartitionKey,
			Payload:      event.Payload,
			Status:       outboxStatusPending,
			NextRetryAt:  time.Now(),
		}).Error
	})
}

// FindByConversationAndClientMsgID 按幂等键回查消息。
func (r *messageRepo) FindByConversationAndClientMsgID(ctx context.Context, conversationID, senderID int64, clientMsgID string) (*biz.Message, error) {
	var m messageModel
	err := r.data.conn(ctx).
		Where("conversation_id = ? AND sender_id = ? AND client_msg_id = ?", conversationID, senderID, clientMsgID).
		First(&m).Error
	if err != nil {
		if isRecordNotFound(err) {
			// 未命中不是错误：上层把它当作「不是重试」的正常分支
			return nil, nil
		}
		return nil, fmt.Errorf("data: 按幂等键查询消息失败: %w", err)
	}
	return toBizMessage(&m), nil
}

// FindLastByConversations 批量取每个会话的最后一条消息。
//
// 用 DISTINCT ON 一次查完：若按会话逐个查，会话列表接口会退化成 N+1，
// 在会话数上百时响应时间会明显劣化。
func (r *messageRepo) FindLastByConversations(ctx context.Context, conversationIDs []int64) (map[int64]*biz.Message, error) {
	result := make(map[int64]*biz.Message, len(conversationIDs))
	if len(conversationIDs) == 0 {
		return result, nil
	}

	var rows []messageModel
	err := r.data.conn(ctx).
		Raw(`SELECT DISTINCT ON (conversation_id) *
		     FROM message
		     WHERE conversation_id IN ?
		     ORDER BY conversation_id, seq DESC`, conversationIDs).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("data: 批量查询会话最后一条消息失败: %w", err)
	}

	for i := range rows {
		result[rows[i].ConversationID] = toBizMessage(&rows[i])
	}
	return result, nil
}

// ListBySeqRange 按序号区间拉取消息。
//
// fromSeq 是开区间下界：客户端已读位点表达的是「这条我读过了」，
// 因此下次拉取应当从它之后开始，否则每次都会重复返回已读的那条。
func (r *messageRepo) ListBySeqRange(ctx context.Context, conversationID, fromSeq, toSeq int64, limit int, ascending bool) ([]*biz.Message, error) {
	if limit <= 0 {
		limit = 20
	}

	q := r.data.conn(ctx).
		Model(&messageModel{}).
		Where("conversation_id = ? AND seq > ?", conversationID, fromSeq)
	if toSeq > 0 {
		q = q.Where("seq <= ?", toSeq)
	}

	order := "seq DESC"
	if ascending {
		order = "seq ASC"
	}

	var rows []messageModel
	if err := q.Order(order).Limit(limit).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("data: 拉取消息失败: %w", err)
	}

	msgs := make([]*biz.Message, 0, len(rows))
	for i := range rows {
		msgs = append(msgs, toBizMessage(&rows[i]))
	}
	return msgs, nil
}

// Recall 撤回消息。
//
// 条件更新一次性表达三个约束：属于该会话、由本人发送、当前处于正常状态。
// 把判断放进 WHERE 而不是先查再改，是因为后者在并发下会让两次撤回都成功。
func (r *messageRepo) Recall(ctx context.Context, conversationID, messageID, operatorID int64) (bool, error) {
	res := r.data.conn(ctx).
		Model(&messageModel{}).
		Where("id = ? AND conversation_id = ? AND sender_id = ? AND status = ?",
			messageID, conversationID, operatorID, biz.MessageStatusNormal).
		Updates(map[string]any{
			"status":      biz.MessageStatusRecalled,
			"recalled_at": time.Now(),
			"recalled_by": operatorID,
		})
	if res.Error != nil {
		return false, fmt.Errorf("data: 撤回消息失败: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// LoadMessage 按会话与序号装载单条消息。
//
// 供消息消费者使用：投递事件里只有 (conversationId, seq)，
// 推送时需要完整消息体。走 uk_message_conv_seq 唯一索引，开销可控。
func (r *messageRepo) LoadMessage(ctx context.Context, conversationID, seq int64) (*biz.Message, error) {
	var m messageModel
	err := r.data.conn(ctx).
		Where("conversation_id = ? AND seq = ?", conversationID, seq).
		First(&m).Error
	if err != nil {
		if isRecordNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("data: 按序号装载消息失败: %w", err)
	}
	return toBizMessage(&m), nil
}

// ── Outbox ──────────────────────────────────────────────────────────────────

// Outbox 状态
//
// 与数据库里的取值一一对应，见 migrations/0001_init.up.sql 的
// message_outbox.status 注释。新增 delivering 是为了让「已捞取、
// 尚未确认」与「待投递」区分开，从而在源头杜绝重复投递。
const (
	outboxStatusPending   int16 = 0
	outboxStatusDelivered int16 = 1
	outboxStatusFailed    int16 = 2
	// outboxStatusDelivering 表示事件已被某个投递协程捞取、正在投递中。
	//
	// 处于该状态的事件不会被再次捞取，因此不会重复投递。
	// 它的风险是「投递中被强杀导致永久滞留」，由 ReclaimStale 按租约兜底。
	outboxStatusDelivering int16 = 3
)

// outboxRepo 是 biz.OutboxRepo 的 GORM 实现。
type outboxRepo struct{ data *Data }

// NewOutboxRepo 构造 Outbox 仓储。
func NewOutboxRepo(d *Data) biz.OutboxRepo { return &outboxRepo{data: d} }

// Append 追加一条事件。
//
// 使用 conn(ctx) 而非 db：调用方可能在事务中，此时必须复用同一个事务句柄，
// 否则事件会被写到事务之外，失去「与消息共存亡」的保证。
func (r *outboxRepo) Append(ctx context.Context, event *biz.OutboxEvent) error {
	err := r.data.conn(ctx).Create(&messageOutboxModel{
		EventID:      event.EventID,
		Topic:        event.Topic,
		PartitionKey: event.PartitionKey,
		Payload:      event.Payload,
		Status:       outboxStatusPending,
		NextRetryAt:  time.Now(),
	}).Error
	if err != nil {
		return fmt.Errorf("data: 追加 Outbox 事件失败: %w", err)
	}
	return nil
}

// FetchPending 捞取一批待投递事件，并把它们置为「投递中」。
//
// 关键在于「捞取」与「置为投递中」在同一事务内完成：
// 若不改状态，下一轮轮询会把同一批事件再捞一遍，导致同一条消息被
// 重复投递到队列、进而被重复推送给用户。这正是「发送者收到两条推送」
// 这类问题的根源——修复它靠的不是让消费者去重，而是让生产者不重复投递。
//
// 用 FOR UPDATE SKIP LOCKED 而不是普通加锁：多实例并发捞取时，
// 每个实例都能跳过已被别人锁住的行，从而各取一批、互不等待。
//
// 已处于「投递中」的事件不会在崩溃后被重新捞取（状态已不是 pending），
// 因此需要 relay 侧的租约超时回收机制来兜底，见 ReclaimStale。
func (r *outboxRepo) FetchPending(ctx context.Context, limit int) ([]*biz.OutboxEvent, error) {
	var events []*biz.OutboxEvent

	err := r.data.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []messageOutboxModel
		if err := tx.
			Clauses(clauseLockingSkipLocked()).
			Where("status = ? AND next_retry_at <= ?", outboxStatusPending, time.Now()).
			Order("next_retry_at ASC, id ASC").
			Limit(limit).
			Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}

		ids := make([]int64, 0, len(rows))
		events = make([]*biz.OutboxEvent, 0, len(rows))
		for i := range rows {
			ids = append(ids, rows[i].ID)
			events = append(events, &biz.OutboxEvent{
				EventID:      rows[i].EventID,
				Topic:        rows[i].Topic,
				PartitionKey: rows[i].PartitionKey,
				Payload:      rows[i].Payload,
				RetryCount:   rows[i].RetryCount,
			})
		}

		// 置为投递中：让重复投递的不可能发生在源头。
		// 条件里再次限定 status = pending，避免与并发的失败回写互相覆盖。
		return tx.Model(&messageOutboxModel{}).
			Where("id IN ? AND status = ?", ids, outboxStatusPending).
			Updates(map[string]any{
				"status":     outboxStatusDelivering,
				"updated_at": time.Now(),
			}).Error
	})
	if err != nil {
		return nil, fmt.Errorf("data: 捞取待投递事件失败: %w", err)
	}
	return events, nil
}

// ReclaimStale 回收「投递中」但已超时的事件。
//
// 「置为投递中」换来了不重复投递，代价是：若实例在投递途中被强杀，
// 该事件会永远停在投递中、再也不会被捞取——这是消息丢失。
// 因此必须有一个兜底：超过租约时间仍未确认的，退回 pending 重新投递。
//
// 这里偏向「重试」而非「放弃」：at-least-once 语义下，
// 重复投递由消费端按 (conversationId, seq) 幂等消化，
// 而漏投递则只能靠客户端拉取补偿，代价更高。
func (r *outboxRepo) ReclaimStale(ctx context.Context, lease time.Duration, limit int) (int, error) {
	res := r.data.db.WithContext(ctx).
		Model(&messageOutboxModel{}).
		Where("status = ? AND updated_at < ?", outboxStatusDelivering, time.Now().Add(-lease)).
		Limit(limit).
		Updates(map[string]any{
			"status":     outboxStatusPending,
			"updated_at": time.Now(),
			"last_error": "投递超时未确认，已回收重投",
		})
	if res.Error != nil {
		return 0, fmt.Errorf("data: 回收超时的投递中事件失败: %w", res.Error)
	}
	return int(res.RowsAffected), nil
}

// MarkDelivered 标记事件已投递（投递确认）。
//
// 条件限定 status = delivering：只有真正被本轮捞取的事件才能被确认，
// 避免迟到的确认把一条已被回收重投的事件又改成已投递，造成漏投递。
func (r *outboxRepo) MarkDelivered(ctx context.Context, eventID string) error {
	err := r.data.conn(ctx).
		Model(&messageOutboxModel{}).
		Where("event_id = ? AND status = ?", eventID, outboxStatusDelivering).
		Updates(map[string]any{
			"status":     outboxStatusDelivered,
			"last_error": "",
			"updated_at": time.Now(),
		}).Error
	if err != nil {
		return fmt.Errorf("data: 标记事件已投递失败: %w", err)
	}
	return nil
}

// MarkFailed 记录失败并安排下次重试。
//
// 同时递增 retry_count，投递方据此计算退避时长；超过上限后
// 由投递方决定是否转为死信，仓储层不替它做这个决策。
//
// 状态退回 pending（而非停在 delivering）：这一轮已经失败，
// 应当让它在退避时间到达后重新参与投递。
func (r *outboxRepo) MarkFailed(ctx context.Context, eventID, reason string, nextRetryAt time.Time) error {
	err := r.data.conn(ctx).
		Model(&messageOutboxModel{}).
		Where("event_id = ?", eventID).
		Updates(map[string]any{
			"status":        outboxStatusPending,
			"retry_count":   gorm.Expr("retry_count + 1"),
			"last_error":    truncate(reason, 255),
			"next_retry_at": nextRetryAt,
			"updated_at":    time.Now(),
		}).Error
	if err != nil {
		return fmt.Errorf("data: 标记事件投递失败时出错: %w", err)
	}
	return nil
}

// MarkDead 标记为死信：重试次数已耗尽，停止自动重试。
//
// 同时把 next_retry_at 推到远未来：即使有人误把状态改回 pending，
// 也不会立刻被捞取，给人工介入留出窗口。
func (r *outboxRepo) MarkDead(ctx context.Context, eventID, reason string) error {
	err := r.data.conn(ctx).
		Model(&messageOutboxModel{}).
		Where("event_id = ?", eventID).
		Updates(map[string]any{
			"status":        outboxStatusFailed,
			"last_error":    truncate(reason, 255),
			"next_retry_at": time.Now().Add(100 * 365 * 24 * time.Hour),
			"updated_at":    time.Now(),
		}).Error
	if err != nil {
		return fmt.Errorf("data: 标记事件为死信失败: %w", err)
	}
	return nil
}

// truncate 按字符截断字符串，避免超出列长度导致写入失败。
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	// 按字节截断后可能切坏 UTF-8 序列，这里退到最近的字符边界
	cut := max
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut]
}

// utf8Start 判断字节是否为 UTF-8 字符的首字节。
func utf8Start(b byte) bool { return b&0xC0 != 0x80 }
