package biz

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	klog "github.com/go-kratos/kratos/v2/log"
)

// 会话类型
const (
	ConversationTypeSingle int32 = 1
	ConversationTypeGroup  int32 = 2
)

// 会话状态
const (
	ConversationStatusNormal  int32 = 1
	ConversationStatusPending int32 = 2
	ConversationStatusBlocked int32 = 3
)

// 成员角色
const (
	MemberRoleMember int32 = 0
	MemberRoleAdmin  int32 = 1
	MemberRoleOwner  int32 = 2
)

// Conversation 是会话实体，单聊与群聊统一建模。
//
// 为什么不拆成两张表：单聊与群聊在"消息容器"这一核心语义上完全一致，
// 消息、成员、已读位点、序号分配都能共用一套逻辑。拆开会导致消息表维护两套外键，
// 且后续"临时会话""群聊转单聊"这类需求会很难做。
type Conversation struct {
	ID   int64
	Type int32
	// BizKey 是业务去重键：单聊为 "minUid_maxUid"，群聊为会话 ID 的字符串形式。
	// 单聊依赖它的唯一约束保证"一对用户只会有一个会话"。
	BizKey    string
	Name      string
	AvatarURL string
	Status    int32
	// OwnerID 仅群聊有意义，单聊为 0
	OwnerID int64
	// MaxSeq 是会话内序号的高水位，用于客户端对齐与服务端补洞
	MaxSeq int64
	// MemberCount 是冗余的成员数，避免每次统计
	MemberCount int32
	CreatedAt   time.Time
}

// ConversationMember 是会话成员，承载成员维度上的状态。
type ConversationMember struct {
	ConversationID int64
	UserID         int64
	// AliasName 是用户对该会话的自定义备注名：单聊为对好友的备注，群聊为我在群里的昵称
	AliasName string
	Role      int32
	// LastReadSeq 是已读位点，只表达「读到哪了」这一事实
	LastReadSeq int64
	// UnreadCount 是独立维护的未读计数。
	//
	// 刻意不用 MaxSeq - LastReadSeq 推导：该差值在退群后重新入群（位点归零）、
	// 消息撤回、消息删除等场景下都不等于真实未读数，而且会让一个高频展示字段
	// 依赖两处数据的实时一致性。
	UnreadCount int64
	JoinedAt    time.Time
	// LeftAt 非空表示已离开该会话。成员行刻意保留而不删除，
	// 以便「谁什么时候退的群」可追溯，也让重新入群能复用同一行。
	LeftAt *time.Time
}

// UserConversation 聚合"某个用户参与的会话"这一关系。
type UserConversation struct {
	Conversation *Conversation
	Member       *ConversationMember
}

// ConversationItem 是会话列表项，已按展示需要组装完毕。
type ConversationItem struct {
	Conversation *Conversation
	// DisplayName / DisplayAvatar 已按会话类型解析：
	// 单聊取对方昵称与头像，群聊取群名与群头像，且优先使用成员备注名。
	DisplayName   string
	DisplayAvatar string
	// LastMessage 可能为 nil（会话刚建立、还没有任何消息）
	LastMessage *Message
	UnreadCount int64
	LastReadSeq int64
}

// 会话业务去重键的前缀。
//
// 为什么带前缀：单聊键与群聊键共用一个唯一索引（type, biz_key），
// 前缀让两种键在肉眼与日志中即可区分；同时避免极端情况下
// 「某个用户 ID 拼出的串」恰好等于另一个群聊键的形态。
const (
	singleBizKeyPrefix = "S:"
	groupBizKeyPrefix  = "G:"
)

// SingleBizKey 生成单聊会话的业务键。
//
// 关键不变量：无论谁发起，A 与 B 之间必须得到同一个键。
// 这里对两个用户 ID 做数值比较后排序，而不是拼完再比较字符串——
// 字符串比较下 "1000" < "999"（逐字符比 '1' < '9'），
// 会让 (1000, 999) 与 (999, 1000) 产生两个不同的键，
// 进而突破唯一索引、出现两个单聊会话。
//
// 该不变量由 TestSingleBizKeySymmetric 用属性测试守护。
func SingleBizKey(uidA, uidB int64) string {
	if uidA > uidB {
		uidA, uidB = uidB, uidA
	}
	return fmt.Sprintf("%s%d:%d", singleBizKeyPrefix, uidA, uidB)
}

// GroupBizKey 生成群聊会话的业务键。
func GroupBizKey(conversationID int64) string {
	return groupBizKeyPrefix + strconv.FormatInt(conversationID, 10)
}

// ConversationRepo 是会话仓储。
type ConversationRepo interface {
	// Create 在单个事务内创建会话与成员。
	// 必须在同一事务中完成，避免出现"有会话却没有成员"的孤儿数据。
	// 调用方需自行设置 conv.ID（见 IDGenerator 的说明）。
	Create(ctx context.Context, conv *Conversation, members []*ConversationMember) error

	FindByID(ctx context.Context, id int64) (*Conversation, error)
	// FindByBizKey 按业务键查询，单聊用于复用已有会话
	FindByBizKey(ctx context.Context, convType int32, bizKey string) (*Conversation, error)

	// ListByUser 返回用户参与的全部会话，按会话最近活跃时间倒序
	ListByUser(ctx context.Context, userID int64) ([]*UserConversation, error)

	// FindPeerIDs 批量查询单聊会话中"对方"的用户 ID，返回 conversationID -> peerUserID。
	// 单独开这个接口是为了避免会话列表对每个单聊逐个查成员造成 N+1。
	FindPeerIDs(ctx context.Context, conversationIDs []int64, selfID int64) (map[int64]int64, error)

	// AddMembers 批量加入成员；已存在的成员自动跳过，返回实际新增数量。
	// 依赖唯一约束实现幂等，因此重复调用是安全的。
	AddMembers(ctx context.Context, conversationID int64, members []*ConversationMember) (int, error)
	RemoveMember(ctx context.Context, conversationID, userID int64) error
	FindMember(ctx context.Context, conversationID, userID int64) (*ConversationMember, error)
	ListMembers(ctx context.Context, conversationID int64) ([]*ConversationMember, error)

	// UpdateMemberReadSeq 仅在传入值更大时推进已读位点。
	// 客户端可能乱序上报，若无条件覆盖会把位点回退，导致已读消息重新变成未读。
	UpdateMemberReadSeq(ctx context.Context, conversationID, userID, seq int64) error
	// UpdateMemberAlias 更新成员在会话中的备注名
	UpdateMemberAlias(ctx context.Context, conversationID, userID int64, alias string) error
	// UpdateMaxSeq 以 CAS 方式推进会话最大序号，保证并发下不会回退
	UpdateMaxSeq(ctx context.Context, conversationID, seq int64) error
	// Update 更新会话自身的属性（群名、群头像、状态等）
	Update(ctx context.Context, conv *Conversation) error
}

// ConversationUseCase 承载会话领域的用例。
type ConversationUseCase struct {
	repo    ConversationRepo
	msgRepo MessageRepo
	users   UserProvider
	idGen   IDGenerator
	log     *klog.Helper
}

// NewConversationUseCase 构造会话用例。
func NewConversationUseCase(
	repo ConversationRepo,
	msgRepo MessageRepo,
	users UserProvider,
	idGen IDGenerator,
	logger klog.Logger,
) *ConversationUseCase {
	return &ConversationUseCase{
		repo:    repo,
		msgRepo: msgRepo,
		users:   users,
		idGen:   idGen,
		log:     klog.NewHelper(klog.With(logger, "module", "biz/conversation")),
	}
}

// ListConversations 返回用户的会话列表，对应消息页首屏。
//
// 组装顺序刻意如此：会话与成员一次查询、对方 ID 一次查询、最后一条消息一次查询、
// 用户资料一次跨服务调用。全程固定 4 次 IO，不随会话数量线性增长。
func (uc *ConversationUseCase) ListConversations(ctx context.Context, userID int64) ([]*ConversationItem, error) {
	if userID <= 0 {
		uc.log.Warnw("msg", "查询会话列表参数非法", "uid", userID)
		return nil, ErrInvalidParam
	}

	relations, err := uc.repo.ListByUser(ctx, userID)
	if err != nil {
		uc.log.Errorw("msg", "查询用户会话列表失败", "uid", userID, "err", err)
		return nil, err
	}
	if len(relations) == 0 {
		uc.log.Debugw("msg", "用户会话列表为空", "uid", userID)
		return nil, nil
	}

	convIDs := make([]int64, 0, len(relations))
	for _, rel := range relations {
		convIDs = append(convIDs, rel.Conversation.ID)
	}

	// 单聊需要知道"对方"是谁才能取到展示名，这里一次性查出全部单聊的对方 ID
	peerOf, err := uc.repo.FindPeerIDs(ctx, convIDs, userID)
	if err != nil {
		uc.log.Errorw("msg", "批量查询单聊对方 ID 失败",
			"uid", userID, "conversations", len(convIDs), "err", err)
		return nil, err
	}

	lastMsgs, err := uc.msgRepo.FindLastByConversations(ctx, convIDs)
	if err != nil {
		uc.log.Errorw("msg", "批量查询会话最后一条消息失败",
			"uid", userID, "conversations", len(convIDs), "err", err)
		return nil, err
	}

	peerIDs := make([]int64, 0, len(peerOf))
	for _, pid := range peerOf {
		peerIDs = append(peerIDs, pid)
	}

	briefs := map[int64]*UserBrief{}
	if len(peerIDs) > 0 {
		briefs, err = uc.users.BatchGetBriefs(ctx, peerIDs)
		if err != nil {
			// 用户资料取不到不应导致整个会话列表失败，降级为无昵称展示并告警
			uc.log.Errorw("msg", "获取用户资料失败，会话列表降级展示", "err", err, "uid", userID)
			briefs = map[int64]*UserBrief{}
		}
	}

	items := make([]*ConversationItem, 0, len(relations))
	for _, rel := range relations {
		item := &ConversationItem{
			Conversation: rel.Conversation,
			LastMessage:  lastMsgs[rel.Conversation.ID],
			LastReadSeq:  rel.Member.LastReadSeq,
			// 直接取成员行上维护的计数，而不是用 MaxSeq - LastReadSeq 推导
			UnreadCount: rel.Member.UnreadCount,
		}
		item.DisplayName, item.DisplayAvatar = resolveDisplay(rel, peerOf[rel.Conversation.ID], briefs)
		items = append(items, item)
	}

	// Debug 级别：会话列表是消息页首屏的最高频接口
	uc.log.Debugw("msg", "会话列表已返回", "uid", userID, "count", len(items))
	return items, nil
}

// GetConversation 返回会话及其成员。
func (uc *ConversationUseCase) GetConversation(ctx context.Context, conversationID, viewerID int64) (*Conversation, []*ConversationMember, error) {
	if conversationID <= 0 {
		uc.log.Warnw("msg", "查询会话详情参数非法", "conversationId", conversationID)
		return nil, nil, ErrInvalidParam
	}

	// 先校验成员身份，避免非成员通过遍历 ID 探测他人会话
	if _, err := uc.mustBeMember(ctx, conversationID, viewerID); err != nil {
		return nil, nil, err
	}

	conv, err := uc.repo.FindByID(ctx, conversationID)
	if err != nil {
		uc.log.Errorw("msg", "查询会话失败",
			"conversationId", conversationID, "viewer", viewerID, "err", err)
		return nil, nil, err
	}
	members, err := uc.repo.ListMembers(ctx, conversationID)
	if err != nil {
		uc.log.Errorw("msg", "查询会话成员列表失败",
			"conversationId", conversationID, "err", err)
		return nil, nil, err
	}
	return conv, members, nil
}

// MarkRead 上报已读位点。
func (uc *ConversationUseCase) MarkRead(ctx context.Context, conversationID, userID, seq int64) error {
	if conversationID <= 0 || seq < 0 {
		uc.log.Warnw("msg", "已读上报参数非法",
			"conversationId", conversationID, "uid", userID, "seq", seq)
		return ErrInvalidParam
	}
	if _, err := uc.mustBeMember(ctx, conversationID, userID); err != nil {
		return err
	}
	if err := uc.repo.UpdateMemberReadSeq(ctx, conversationID, userID, seq); err != nil {
		return err
	}
	// 用 Debug 级别：已读上报由客户端高频触发（每次进入会话、滚动到底部），
	// 按 Info 记录会迅速淹没真正有价值的日志。
	uc.log.Debugw("msg", "已读位点已推进", "conversationId", conversationID, "uid", userID, "seq", seq)
	return nil
}

// EnsureSingleConversation 确保两个用户之间的单聊会话存在，不存在则创建。
//
// 供好友申请通过后调用。依赖 (type, biz_key) 唯一约束兜底并发：
// 若两个请求同时创建，只有一个能成功，另一个命中唯一冲突后回查即可拿到已有会话，
// 因此对外表现是幂等的。
func (uc *ConversationUseCase) EnsureSingleConversation(ctx context.Context, uidA, uidB int64) (*Conversation, error) {
	if uidA <= 0 || uidB <= 0 || uidA == uidB {
		return nil, ErrInvalidParam
	}

	bizKey := SingleBizKey(uidA, uidB)
	if conv, err := uc.repo.FindByBizKey(ctx, ConversationTypeSingle, bizKey); err == nil {
		return conv, nil
	}

	// ID 必须在插入前生成：它同时被用作业务去重键的组成部分与后续消息的分区键
	id, err := uc.idGen.Next()
	if err != nil {
		return nil, err
	}

	conv := &Conversation{
		ID:     id,
		Type:   ConversationTypeSingle,
		BizKey: bizKey,
		Status: ConversationStatusNormal,
		// 单聊恒为两名成员，直接写死避免一次多余的统计
		MemberCount: 2,
	}
	members := []*ConversationMember{
		{UserID: uidA, Role: MemberRoleMember},
		{UserID: uidB, Role: MemberRoleMember},
	}

	if err := uc.repo.Create(ctx, conv, members); err != nil {
		// 并发创建时唯一冲突即代表"别人已经建好了"，回查一次即可
		if existing, ferr := uc.repo.FindByBizKey(ctx, ConversationTypeSingle, bizKey); ferr == nil {
			uc.log.Infow("msg", "单聊会话并发创建，复用已存在的会话",
				"conversationId", existing.ID, "uidA", uidA, "uidB", uidB)
			return existing, nil
		}
		// 到这里说明不是并发冲突，而是真实失败（如建表缺失、连接断开）
		uc.log.Errorw("msg", "创建单聊会话失败",
			"uidA", uidA, "uidB", uidB, "bizKey", bizKey, "err", err)
		return nil, err
	}

	uc.log.Infow("msg", "单聊会话已创建", "conversationId", conv.ID, "uidA", uidA, "uidB", uidB)
	return conv, nil
}

// mustBeMember 校验用户确实是会话成员。
//
// 这是权限体系的关键收口点：所有涉及会话内数据的操作都经它把关，
// 因此把「谁被拒了」记成 Warn —— 偶发是客户端 bug，突增则可能是越权探测。
func (uc *ConversationUseCase) mustBeMember(ctx context.Context, conversationID, userID int64) (*ConversationMember, error) {
	member, err := uc.repo.FindMember(ctx, conversationID, userID)
	if err != nil {
		uc.log.Errorw("msg", "查询会话成员失败",
			"conversationId", conversationID, "uid", userID, "err", err)
		return nil, err
	}
	if member == nil {
		uc.log.Warnw("msg", "非会话成员访问被拒",
			"conversationId", conversationID, "uid", userID)
		return nil, ErrNotConversationMember
	}
	return member, nil
}

// ResolveConversationID 把对外的会话标识解析为会话 ID。
//
// 支持两种历史形态，这是刻意保留的兼容能力：
//   - 纯数字：直接用会话 ID（新契约）
//   - "minUid_maxUid"：早期单聊用双方 ID 拼接作为标识，需要按业务键反查
//
// 把解析逻辑放在领域层而不是 HTTP 适配层，是为了让它可被单元测试覆盖——
// 兼容逻辑最容易随版本演进出隐蔽问题。
func (uc *ConversationUseCase) ResolveConversationID(ctx context.Context, raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		uc.log.Warnw("msg", "会话标识为空")
		return 0, ErrInvalidParam
	}

	if id, err := strconv.ParseInt(raw, 10, 64); err == nil && id > 0 {
		return id, nil
	}

	// 走到这里说明不是纯数字，尝试按单聊业务键回查（兼容旧的 "minUid_maxUid" 形态）
	if conv, err := uc.repo.FindByBizKey(ctx, ConversationTypeSingle, raw); err == nil {
		uc.log.Debugw("msg", "按业务键解析出会话 ID",
			"bizKey", raw, "conversationId", conv.ID)
		return conv.ID, nil
	}
	// Warn 级别：解析失败通常是客户端传了过期的会话标识，
	// 突增时说明客户端缓存或协议对接有问题。
	uc.log.Warnw("msg", "无法解析会话标识", "raw", raw)
	return 0, ErrConversationNotFound
}

// PeerOfSingle 返回单聊会话中的另一方（对方）。
//
// 供兼容旧协议使用：早期接口用单聊会话标识来定位好友申请，
// 需要据此推断出申请双方。
func (uc *ConversationUseCase) PeerOfSingle(ctx context.Context, conversationID, selfID int64) (int64, error) {
	if conversationID <= 0 {
		uc.log.Warnw("msg", "查询单聊对方参数非法",
			"conversationId", conversationID, "self", selfID)
		return 0, ErrInvalidParam
	}
	peers, err := uc.repo.FindPeerIDs(ctx, []int64{conversationID}, selfID)
	if err != nil {
		uc.log.Errorw("msg", "查询单聊对方失败",
			"conversationId", conversationID, "self", selfID, "err", err)
		return 0, err
	}
	peer, ok := peers[conversationID]
	if !ok || peer <= 0 {
		uc.log.Warnw("msg", "单聊会话不存在对方",
			"conversationId", conversationID, "self", selfID)
		return 0, ErrConversationNotFound
	}
	return peer, nil
}

// resolveDisplay 解析会话的展示名与展示头像。
//
// 优先级：成员备注名 > 对方昵称 / 群名。取不到用户资料时用 ID 兜底，
// 保证前端不会出现完全空白的会话项。
func resolveDisplay(rel *UserConversation, peerID int64, briefs map[int64]*UserBrief) (string, string) {
	if rel.Member != nil && rel.Member.AliasName != "" {
		return rel.Member.AliasName, rel.Conversation.AvatarURL
	}

	if rel.Conversation.Type == ConversationTypeSingle {
		if brief, ok := briefs[peerID]; ok && brief != nil && brief.Nickname != "" {
			return brief.Nickname, brief.AvatarURL
		}
		if peerID > 0 {
			return fmt.Sprintf("用户%d", peerID), ""
		}
		return "未知用户", ""
	}

	name := rel.Conversation.Name
	if name == "" {
		name = fmt.Sprintf("群聊%d", rel.Conversation.ID)
	}
	return name, rel.Conversation.AvatarURL
}
