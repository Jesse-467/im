package biz

import (
	"context"
	"errors"
	"strings"
	"time"

	klog "github.com/go-kratos/kratos/v2/log"
)

// 好友申请状态
const (
	FriendRequestPending  int32 = 0
	FriendRequestAccepted int32 = 1
	FriendRequestRejected int32 = 2
	FriendRequestExpired  int32 = 3
)

// 好友关系状态
const (
	FriendStatusNormal  int32 = 1
	FriendStatusBlocked int32 = 2
)

// friendRequestTTL 是好友申请的有效期。
//
// 设过期而不是永久保留：申请表的唯一约束是 (from, to, status)，
// 若待处理申请永不过期，用户被拒绝后想再次申请就会被唯一约束挡死。
const friendRequestTTL = 7 * 24 * time.Hour

// FriendRelation 是好友关系，双向各存一行。
//
// 为什么双向存两行而不是存一行加排序：查询"我的好友列表"是最热的路径，
// 双向存行可以让查询退化为 user_id = ? 的单条件索引扫描；
// 若只存一行，查询条件会变成 user_id = ? OR friend_id = ?，无法有效利用索引。
type FriendRelation struct {
	UserID    int64
	FriendID  int64
	Remark    string
	Status    int32
	CreatedAt time.Time
}

// FriendRequest 是好友申请。
type FriendRequest struct {
	ID        int64
	FromUID   int64
	ToUID     int64
	ApplyMsg  string
	Status    int32
	ExpireAt  time.Time
	HandledAt time.Time
	CreatedAt time.Time
}

// FriendDetail 是好友列表项，含展示所需的用户资料。
type FriendDetail struct {
	UserID int64
	Remark string
	Brief  *UserBrief
}

// FriendRepo 是好友仓储。
type FriendRepo interface {
	// CreateRelation 在单个事务内双向写入好友关系（两行），保证不会只成功一边。
	// 重复调用应幂等（依赖唯一约束跳过已存在的行）。
	CreateRelation(ctx context.Context, uidA, uidB int64, remark string) error
	// AreFriends 判断双方是否为好友。任一方处于拉黑状态都视为非好友。
	AreFriends(ctx context.Context, uidA, uidB int64) (bool, error)
	ListRelations(ctx context.Context, userID int64) ([]*FriendRelation, error)
	// DeleteRelation 双向删除好友关系
	DeleteRelation(ctx context.Context, userID, friendID int64) error
	// SetBlocked 设置拉黑状态（仅作用于 user 自己的那一行）
	SetBlocked(ctx context.Context, userID, targetID int64, blocked bool) error

	// CreateRequest 创建好友申请
	CreateRequest(ctx context.Context, req *FriendRequest) error
	FindRequestByID(ctx context.Context, id int64) (*FriendRequest, error)
	// FindPendingRequest 查询 from -> to 的待处理申请，不存在时返回 nil, nil
	FindPendingRequest(ctx context.Context, fromUID, toUID int64) (*FriendRequest, error)
	// UpdateRequestStatus 推进申请状态。
	//
	// 返回的 bool 表示"本次调用是否真的改动了状态"：实现必须以
	// `WHERE id = ? AND status = 待处理` 作为条件，从而保证并发重复处理时
	// 只有一个请求能成功，另一个得到 false，避免重复建会话。
	UpdateRequestStatus(ctx context.Context, id int64, status int32, handledAt time.Time) (bool, error)
	// ListRequests 查询用户收到的好友申请，status 为 -1 表示不过滤状态
	ListRequests(ctx context.Context, userID int64, status int32, limit int) ([]*FriendRequest, error)
}

// FriendUseCase 承载好友领域的用例。
type FriendUseCase struct {
	repo   FriendRepo
	convUC *ConversationUseCase
	users  UserProvider
	log    *klog.Helper
}

// NewFriendUseCase 构造好友用例。
//
// 依赖 ConversationUseCase 是因为"申请通过"这个动作必须原子地建立单聊会话，
// 这是好友领域自己的业务规则，属于同层协作，不构成跨层依赖。
func NewFriendUseCase(
	repo FriendRepo,
	convUC *ConversationUseCase,
	users UserProvider,
	logger klog.Logger,
) *FriendUseCase {
	return &FriendUseCase{
		repo:   repo,
		convUC: convUC,
		users:  users,
		log:    klog.NewHelper(klog.With(logger, "module", "biz/friend")),
	}
}

// ApplyFriend 发起好友申请。
//
// 返回的 alreadyFriends 用于告知调用方"你们已经是好友了"，此时不会创建新申请。
func (uc *FriendUseCase) ApplyFriend(ctx context.Context, fromUID, toUID int64, applyMsg string) (*FriendRequest, bool, error) {
	if fromUID <= 0 || toUID <= 0 {
		return nil, false, ErrInvalidParam
	}
	if fromUID == toUID {
		return nil, false, ErrCannotAddSelf
	}
	if len([]rune(applyMsg)) > 100 {
		return nil, false, ErrInvalidParam
	}

	if friends, err := uc.repo.AreFriends(ctx, fromUID, toUID); err != nil {
		return nil, false, err
	} else if friends {
		return nil, true, nil
	}

	// 已有待处理申请时直接复用，避免用户连点产生一堆申请
	if pending, err := uc.repo.FindPendingRequest(ctx, fromUID, toUID); err != nil {
		return nil, false, err
	} else if pending != nil {
		if time.Now().Before(pending.ExpireAt) {
			return pending, false, nil
		}
		// 已过期的申请先置为过期，把 (from, to, 待处理) 这个唯一键让出来
		if _, err := uc.repo.UpdateRequestStatus(ctx, pending.ID, FriendRequestExpired, time.Now()); err != nil {
			return nil, false, err
		}
	}

	req := &FriendRequest{
		FromUID:  fromUID,
		ToUID:    toUID,
		ApplyMsg: strings.TrimSpace(applyMsg),
		Status:   FriendRequestPending,
		ExpireAt: time.Now().Add(friendRequestTTL),
	}
	if err := uc.repo.CreateRequest(ctx, req); err != nil {
		return nil, false, err
	}

	uc.log.Infow("msg", "好友申请已创建", "from", fromUID, "to", toUID, "requestId", req.ID)
	return req, false, nil
}

// HandleFriend 处理好友申请。
//
// 同意时的动作必须作为一个整体看待：推进申请状态 → 建立双向好友关系 → 创建单聊会话。
// 其中状态推进采用条件更新，只有胜出的那一个请求才会继续后续步骤，
// 因此并发重复提交同一条申请不会产生两个会话。
func (uc *FriendUseCase) HandleFriend(ctx context.Context, operatorID, requestID int64, agree bool) (int64, error) {
	if operatorID <= 0 || requestID <= 0 {
		return 0, ErrInvalidParam
	}

	req, err := uc.repo.FindRequestByID(ctx, requestID)
	if err != nil {
		return 0, err
	}
	// 只有收件人可以处理自己的申请
	if req.ToUID != operatorID {
		return 0, ErrNoPermission
	}
	if req.Status != FriendRequestPending {
		return 0, ErrFriendRequestHandled
	}

	target := FriendRequestRejected
	if agree {
		target = FriendRequestAccepted
	}
	changed, err := uc.repo.UpdateRequestStatus(ctx, requestID, target, time.Now())
	if err != nil {
		return 0, err
	}
	if !changed {
		// 另一个并发请求已经处理过了
		return 0, ErrFriendRequestHandled
	}

	if !agree {
		uc.log.Infow("msg", "好友申请被拒绝", "requestId", requestID, "operator", operatorID)
		return 0, nil
	}

	if err := uc.repo.CreateRelation(ctx, req.FromUID, operatorID, ""); err != nil {
		return 0, err
	}

	conv, err := uc.convUC.EnsureSingleConversation(ctx, req.FromUID, operatorID)
	if err != nil {
		// 会话创建失败不回滚好友关系：好友关系已生效，会话可以由客户端
		// 首次发消息时按需补建（EnsureSingleConversation 是幂等的），
		// 反之回滚则会让"已通过"的申请处于不一致状态。
		uc.log.Errorw("msg", "好友已建立但单聊会话创建失败", "err", err, "requestId", requestID)
		return 0, nil
	}

	uc.log.Infow("msg", "好友申请已通过", "requestId", requestID, "conversationId", conv.ID)
	return conv.ID, nil
}

// ListFriendRequests 查询收到的好友申请。
func (uc *FriendUseCase) ListFriendRequests(ctx context.Context, userID int64, status int32, limit int) ([]*FriendRequest, error) {
	if userID <= 0 {
		return nil, ErrInvalidParam
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return uc.repo.ListRequests(ctx, userID, status, limit)
}

// ListFriends 返回好友列表，并补齐展示所需的昵称与头像。
func (uc *FriendUseCase) ListFriends(ctx context.Context, userID int64) ([]*FriendDetail, error) {
	if userID <= 0 {
		return nil, ErrInvalidParam
	}

	relations, err := uc.repo.ListRelations(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(relations) == 0 {
		return nil, nil
	}

	friendIDs := make([]int64, 0, len(relations))
	for _, rel := range relations {
		friendIDs = append(friendIDs, rel.FriendID)
	}

	briefs := map[int64]*UserBrief{}
	if briefs, err = uc.users.BatchGetBriefs(ctx, friendIDs); err != nil {
		uc.log.Errorw("msg", "获取好友资料失败，列表降级展示", "err", err, "uid", userID)
		briefs = map[int64]*UserBrief{}
	}

	details := make([]*FriendDetail, 0, len(relations))
	for _, rel := range relations {
		details = append(details, &FriendDetail{
			UserID: rel.FriendID,
			Remark: rel.Remark,
			Brief:  briefs[rel.FriendID],
		})
	}
	return details, nil
}

// AreFriends 判断两人是否为好友，供消息发送前的校验使用。
func (uc *FriendUseCase) AreFriends(ctx context.Context, uidA, uidB int64) (bool, error) {
	return uc.repo.AreFriends(ctx, uidA, uidB)
}

// DeleteFriend 删除好友关系。仅解除关系，历史消息与会话保留，
// 这样单聊记录不会因为删好友而凭空消失。
func (uc *FriendUseCase) DeleteFriend(ctx context.Context, userID, friendID int64) error {
	if userID <= 0 || friendID <= 0 || userID == friendID {
		return ErrInvalidParam
	}
	return uc.repo.DeleteRelation(ctx, userID, friendID)
}

// BlockUser 拉黑或取消拉黑某个用户。
func (uc *FriendUseCase) BlockUser(ctx context.Context, userID, targetID int64, blocked bool) error {
	if userID <= 0 || targetID <= 0 || userID == targetID {
		return ErrInvalidParam
	}
	if blocked {
		if friends, err := uc.repo.AreFriends(ctx, userID, targetID); err != nil {
			return err
		} else if !friends {
			return ErrNotFriend
		}
	}
	return uc.repo.SetBlocked(ctx, userID, targetID, blocked)
}

// FindPendingRequestBetween 查询两人之间的待处理申请。
//
// 供 service 层做旧接口兼容：旧协议没有 requestId，只给了单聊会话 ID，
// 需要据此反查出对应的申请。
func (uc *FriendUseCase) FindPendingRequestBetween(ctx context.Context, fromUID, toUID int64) (*FriendRequest, error) {
	return uc.repo.FindPendingRequest(ctx, fromUID, toUID)
}

// FindRequestByID 按 ID 查询好友申请。
func (uc *FriendUseCase) FindRequestByID(ctx context.Context, id int64) (*FriendRequest, error) {
	if id <= 0 {
		return nil, ErrInvalidParam
	}
	return uc.repo.FindRequestByID(ctx, id)
}

// IsRequestHandled 判断申请是否已处理，用于把"已处理"与"不存在"区分开。
func IsRequestHandled(err error) bool {
	return errors.Is(err, ErrFriendRequestHandled)
}
