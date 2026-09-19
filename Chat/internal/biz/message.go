package biz

import (
	"context"
	"fmt"
	"strconv"
	"time"

	klog "github.com/go-kratos/kratos/v2/log"
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

// 消息拉取与发送的容量约束
const (
	// pullDefaultLimit 是拉取时的默认条数
	pullDefaultLimit = 20
	// pullMaxLimit 是单次拉取的上限。
	// 必须有上限：否则客户端传 limit=100000 就会把整段历史一次性拉走，
	// 既拖垮数据库也拖垮客户端。
	pullMaxLimit = 200
	// messageContentMaxLen 限制单条消息正文长度
	messageContentMaxLen = 4096
	// clientMsgIDMaxLen 与表结构保持一致
	clientMsgIDMaxLen = 64
)

// Message 是消息实体。
type Message struct {
	ID             int64
	ConversationID int64
	// Seq 是会话内全局单调递增的序号，是消息顺序的权威依据。
	// 之所以不用自增主键：分表后主键不再全局有序，且跨会话比较没有意义。
	Seq int64
	// SenderSeq 是发送者在本会话内的序号。
	// 它存在的意义是让「同一发送者的插入」具备数据库级幂等：
	// 仅靠 client_msg_id 的话，客户端换一个 ID 重试就能绕过幂等约束。
	SenderSeq int64
	SenderID  int64
	Type      int32
	Content   string
	// Extra 承载富媒体元信息等扩展内容，以 JSON 字符串原样透传
	Extra string
	// ClientMsgID 是客户端幂等键，重试发送不会产生重复消息
	ClientMsgID string
	Status      int32
	// RecalledAt / RecalledBy 记录撤回操作，用于合规追溯
	RecalledAt time.Time
	RecalledBy int64
	CreatedAt  time.Time
}

// SeqAllocator 分配会话内序号。
//
// 抽成接口而非直接依赖 Redis 或数据库：发号方案会随规模演进
// （本地单机 → Redis → 分片计数器 → 独立发号服务），业务层不该随之改动。
type SeqAllocator interface {
	// Next 为指定会话分配一个严格递增的序号。
	Next(ctx context.Context, conversationID int64) (int64, error)
	// NextSenderSeq 为「某会话中的某发送者」分配序号。
	//
	// 之所以要单独发号而不是统计该用户已发条数：并发下统计会得到相同的值，
	// 触发唯一约束。发号器是关键路径上唯一能保证唯一性的地方。
	NextSenderSeq(ctx context.Context, conversationID, senderID int64) (int64, error)
}

// OutboxEvent 是一条待投递的领域事件。
//
// 事件与消息放在同一事务写入，因此「消息落库」与「事件可投递」是原子的，
// 不会出现消息已存在但永远不会被推送的情况。
type OutboxEvent struct {
	EventID string
	Topic   string
	// PartitionKey 取会话 ID：同会话的消息进入同一分区，从而保证投递顺序
	PartitionKey string
	Payload      []byte
	// RetryCount 是已重试次数，投递方据此计算退避时长与是否转为死信
	RetryCount int
}

// OutboxRepo 是本地消息表（Outbox）仓储。
type OutboxRepo interface {
	// Append 在给定事务上下文中追加一条事件。
	//
	// 注意：本方法必须能在调用方开启的事务里执行，因此实现要使用
	// ctx 中携带的事务句柄，而不是自己新开事务。
	Append(ctx context.Context, event *OutboxEvent) error

	// FetchPending 捞取一批待投递事件，并把它们置为「投递中」。
	//
	// 实现必须满足两点：
	//   - 使用 FOR UPDATE SKIP LOCKED，使多个投递协程/多实例可以并行处理，
	//     既不互相阻塞，也不会重复捞取同一行；
	//   - 在同一事务内把捞取到的事件标记为「投递中」，否则下一轮轮询会再次
	//     捞到它们，导致同一条消息被重复投递、进而重复推送给用户。
	FetchPending(ctx context.Context, limit int) ([]*OutboxEvent, error)

	// ReclaimStale 回收「投递中」但超过租约仍未确认的事件。
	//
	// 这是「置为投递中」的必要配套：投递协程被强杀时事件会滞留在投递中，
	// 没有本方法它将永远不会被重投，等价于消息丢失。
	// 返回本次回收的条数。
	ReclaimStale(ctx context.Context, lease time.Duration, limit int) (int, error)

	// MarkDelivered 标记投递成功（投递确认）
	MarkDelivered(ctx context.Context, eventID string) error
	// MarkFailed 标记投递失败并安排下次重试
	MarkFailed(ctx context.Context, eventID, reason string, nextRetryAt time.Time) error
	// MarkDead 标记为死信，不再自动重试。
	//
	// 与 MarkFailed 的区别：MarkFailed 是「这次失败了，过会儿再试」，
	// 本方法是「重试次数已耗尽，停止自动重试，等人工介入」。
	// 没有这个终态的话，事件会在 pending 与失败之间无限循环，
	// 既浪费资源，也让监控失去意义。
	MarkDead(ctx context.Context, eventID, reason string) error
}

// MessageRepo 是消息仓储。
type MessageRepo interface {
	// Create 在单个事务内写入消息与对应的 Outbox 事件。
	//
	// 必须原子：先写消息再写事件的话，进程在两步之间崩溃就会产生
	// 「消息存在但从未投递」的静默丢失。
	Create(ctx context.Context, msg *Message, event *OutboxEvent) error

	// FindByConversationAndClientMsgID 用于幂等命中时回查已存在的消息
	FindByConversationAndClientMsgID(ctx context.Context, conversationID, senderID int64, clientMsgID string) (*Message, error)

	// FindLastByConversations 批量取每个会话的最后一条消息。
	// 必须一次性查询，避免会话列表出现 N+1。
	FindLastByConversations(ctx context.Context, conversationIDs []int64) (map[int64]*Message, error)

	// ListBySeqRange 按序号区间拉取消息。
	//
	// fromSeq 为开区间下界，toSeq 为闭区间上界（0 表示不限），
	// ascending 决定返回顺序：离线补偿要升序，历史翻页要降序。
	ListBySeqRange(ctx context.Context, conversationID, fromSeq, toSeq int64, limit int, ascending bool) ([]*Message, error)

	// Recall 撤回消息，并把 recalled_at/recalled_by 一并写入。
	//
	// 实现必须用条件更新（status = 正常）保证只有一次撤回能生效，
	// 同时限定 sender_id 防止撤回他人消息。
	Recall(ctx context.Context, conversationID, messageID, operatorID int64) (bool, error)
}

// MessageUseCase 承载消息领域的用例。
type MessageUseCase struct {
	repo     MessageRepo
	convRepo ConversationRepo
	convUC   *ConversationUseCase
	seq      SeqAllocator
	idGen    IDGenerator
	topic    string
	log      *klog.Helper
}

// NewMessageUseCase 构造消息用例。
func NewMessageUseCase(
	repo MessageRepo,
	convRepo ConversationRepo,
	convUC *ConversationUseCase,
	seq SeqAllocator,
	idGen IDGenerator,
	topic MessageTopic,
	logger klog.Logger,
) *MessageUseCase {
	return &MessageUseCase{
		repo:     repo,
		convRepo: convRepo,
		convUC:   convUC,
		seq:      seq,
		idGen:    idGen,
		topic:    string(topic),
		log:      klog.NewHelper(klog.With(logger, "module", "biz/message")),
	}
}

// SendResult 是发送消息的结果。
type SendResult struct {
	Message *Message
	// Duplicated 为 true 表示命中幂等键，返回的是此前已存在的消息。
	// 调用方据此可以区分「新消息」与「重试」，重试时不应重复广播。
	Duplicated bool
}

// Send 发送消息。
//
// 执行顺序是刻意的：
//  1. 先做权限与参数校验（失败时不必消耗序号）；
//  2. 再查幂等（重试请求不应消耗序号，否则会留下空洞）；
//  3. 最后发号并落库。
//
// 发号与落库的顺序也经过权衡：先发号再落库，若落库失败会浪费一个序号，
// 表现为会话中出现序号空洞。这是可接受的——客户端按 seq 补洞时对空洞
// 的容忍度远高于对乱序的容忍度，而若先落库再发号则必须引入两阶段提交。
func (uc *MessageUseCase) Send(ctx context.Context, req *SendMessageRequest) (*SendResult, error) {
	if err := uc.validateSendRequest(req); err != nil {
		return nil, err
	}

	// 成员校验：非成员不得向会话发消息
	if _, err := uc.convRepo.FindMember(ctx, req.ConversationID, req.SenderID); err != nil {
		return nil, err
	}

	clientMsgID := req.ClientMsgID
	if clientMsgID == "" {
		// 客户端未提供幂等键时由服务端生成，保证该列不为空
		id, err := uc.idGen.Next()
		if err != nil {
			return nil, err
		}
		clientMsgID = "srv-" + strconv.FormatInt(id, 36)
	}

	// 幂等回查：命中则直接返回既有消息，不再发号、不再落库、不再投递
	if existing, err := uc.repo.FindByConversationAndClientMsgID(ctx, req.ConversationID, req.SenderID, clientMsgID); err == nil && existing != nil {
		return &SendResult{Message: existing, Duplicated: true}, nil
	}

	msgID, err := uc.idGen.Next()
	if err != nil {
		return nil, err
	}

	seq, err := uc.seq.Next(ctx, req.ConversationID)
	if err != nil {
		return nil, err
	}

	senderSeq, err := uc.seq.NextSenderSeq(ctx, req.ConversationID, req.SenderID)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	msg := &Message{
		ID:             msgID,
		ConversationID: req.ConversationID,
		Seq:            seq,
		SenderSeq:      senderSeq,
		SenderID:       req.SenderID,
		Type:           req.Type,
		Content:        req.Content,
		Extra:          req.Extra,
		ClientMsgID:    clientMsgID,
		Status:         MessageStatusNormal,
		CreatedAt:      now,
	}

	event, err := uc.buildOutboxEvent(msg)
	if err != nil {
		return nil, err
	}

	if err := uc.repo.Create(ctx, msg, event); err != nil {
		// 并发重试时可能同时通过了幂等回查，此时唯一约束会拦下后到的那个。
		// 这不是错误，按幂等语义返回已存在的消息即可。
		if existing, ferr := uc.repo.FindByConversationAndClientMsgID(ctx, req.ConversationID, req.SenderID, clientMsgID); ferr == nil && existing != nil {
			return &SendResult{Message: existing, Duplicated: true}, nil
		}
		return nil, err
	}

	// 未读数在投递链路上按成员累加；此处只负责把消息本身写正确。
	uc.log.Infow(
		"msg", "消息已发送",
		"conversationId", msg.ConversationID,
		"messageId", msg.ID,
		"seq", msg.Seq,
		"senderId", msg.SenderID,
	)

	return &SendResult{Message: msg}, nil
}

// Pull 拉取消息。
//
// 两种典型用法：
//   - 离线补偿：ascending=true，从客户端已读位点往后补齐；
//   - 历史翻页：ascending=false，从最旧一条往前翻。
func (uc *MessageUseCase) Pull(ctx context.Context, req *PullMessagesRequest) (*PullMessagesResult, error) {
	if req.ConversationID <= 0 || req.UserID <= 0 {
		return nil, ErrInvalidParam
	}
	if _, err := uc.convRepo.FindMember(ctx, req.ConversationID, req.UserID); err != nil {
		return nil, err
	}

	limit := req.Limit
	if limit <= 0 {
		limit = pullDefaultLimit
	}
	if limit > pullMaxLimit {
		limit = pullMaxLimit
	}

	conv, err := uc.convRepo.FindByID(ctx, req.ConversationID)
	if err != nil {
		return nil, err
	}

	msgs, err := uc.repo.ListBySeqRange(ctx, req.ConversationID, req.FromSeq, req.ToSeq, limit, req.Ascending)
	if err != nil {
		return nil, err
	}

	return &PullMessagesResult{
		Messages: msgs,
		// MaxSeq 让客户端拿到最新水位，便于判断自己是否落后
		MaxSeq: conv.MaxSeq,
		// 取满 limit 即认为可能还有更多，客户端可据此继续拉取。
		// 这里刻意不做 count(*) 之类的精确判断，避免多一次全表扫描。
		HasMore: len(msgs) == limit,
	}, nil
}

// Recall 撤回消息。
//
// 只有发送者本人可以撤回。是否允许超时撤回（如 2 分钟限制）属于产品策略，
// 这里不内置，由调用方在需要时自行加时间判断——把它写死在领域层会让
// 不同业务线无法差异化配置。
func (uc *MessageUseCase) Recall(ctx context.Context, conversationID, messageID, operatorID int64) error {
	if conversationID <= 0 || messageID <= 0 || operatorID <= 0 {
		return ErrInvalidParam
	}
	if _, err := uc.convRepo.FindMember(ctx, conversationID, operatorID); err != nil {
		return err
	}

	ok, err := uc.repo.Recall(ctx, conversationID, messageID, operatorID)
	if err != nil {
		return err
	}
	if !ok {
		// 条件更新影响 0 行：要么消息不存在，要么不是本人发送，要么已被撤回
		return ErrMessageNotRecallable
	}

	uc.log.Infow("msg", "消息已撤回", "conversationId", conversationID, "messageId", messageID, "operator", operatorID)
	return nil
}

// validateSendRequest 校验发送请求。
func (uc *MessageUseCase) validateSendRequest(req *SendMessageRequest) error {
	if req == nil {
		return ErrInvalidParam
	}
	if req.ConversationID <= 0 || req.SenderID <= 0 {
		return ErrInvalidParam
	}
	switch req.Type {
	case MessageTypeText, MessageTypeImage, MessageTypeVideo, MessageTypeAudio, MessageTypeSystem:
	default:
		return ErrInvalidParam
	}
	if len([]rune(req.Content)) > messageContentMaxLen {
		return ErrInvalidParam
	}
	if len(req.ClientMsgID) > clientMsgIDMaxLen {
		return ErrInvalidParam
	}
	return nil
}

// buildOutboxEvent 由消息构造投递事件。
func (uc *MessageUseCase) buildOutboxEvent(msg *Message) (*OutboxEvent, error) {
	eventID, err := uc.idGen.Next()
	if err != nil {
		return nil, err
	}

	// 事件体刻意做成精简结构：投递方只需知道「哪条会话的哪条消息」，
	// 完整消息由接收端按 ID/seq 回查，这样后续新增消息字段不需要改动事件格式。
	payload := fmt.Sprintf(
		`{"conversationId":%d,"messageId":%d,"seq":%d,"senderId":%d,"type":%d}`,
		msg.ConversationID, msg.ID, msg.Seq, msg.SenderID, msg.Type,
	)

	return &OutboxEvent{
		EventID:      strconv.FormatInt(eventID, 36),
		Topic:        uc.topic,
		PartitionKey: strconv.FormatInt(msg.ConversationID, 10),
		Payload:      []byte(payload),
	}, nil
}

// PullMessagesRequest 是拉取请求。
type PullMessagesRequest struct {
	ConversationID int64
	UserID         int64
	FromSeq        int64
	ToSeq          int64
	Limit          int
	Ascending      bool
}

// PullMessagesResult 是拉取结果。
type PullMessagesResult struct {
	Messages []*Message
	HasMore  bool
	MaxSeq   int64
}

// SendMessageRequest 是发送请求。
type SendMessageRequest struct {
	ConversationID int64
	SenderID       int64
	Type           int32
	Content        string
	Extra          string
	ClientMsgID    string
}
