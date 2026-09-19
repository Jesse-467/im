package biz

import (
	"context"
	"strings"
	"time"

	klog "github.com/go-kratos/kratos/v2/log"
)

// 群名称长度约束
const (
	groupNameMaxLen = 50
	groupMaxMembers = 500
)

// GroupMemberDetail 是群成员详情，含展示所需的用户资料。
type GroupMemberDetail struct {
	Member *ConversationMember
	Brief  *UserBrief
}

// GroupUseCase 承载群聊领域的用例。
//
// 群聊没有独立的数据表：它复用会话模型（type = 群聊），
// 因此这里只做群语义特有的规则校验，数据读写全部委托给 ConversationUseCase 与仓储。
type GroupUseCase struct {
	convRepo ConversationRepo
	convUC   *ConversationUseCase
	users    UserProvider
	idGen    IDGenerator
	log      *klog.Helper
}

// NewGroupUseCase 构造群聊用例。
func NewGroupUseCase(
	convRepo ConversationRepo,
	convUC *ConversationUseCase,
	users UserProvider,
	idGen IDGenerator,
	logger klog.Logger,
) *GroupUseCase {
	return &GroupUseCase{
		convRepo: convRepo,
		convUC:   convUC,
		users:    users,
		idGen:    idGen,
		log:      klog.NewHelper(klog.With(logger, "module", "biz/group")),
	}
}

// CreateGroup 创建群聊。
//
// memberIDs 中的无效项（自己、重复、不存在的用户）会被静默跳过，
// 返回值中的 addedCount 是实际加入的人数，便于调用方感知差异。
//
// 说明：这里刻意不校验"邀请人与被邀请人必须是好友"。群聊的常见用法是把
// 同事、客户等尚未建立好友关系的人拉进同一个讨论组，强制好友关系会让
// 这一核心场景无法使用；防刷由"操作者必须是群成员"这一前提来约束。
func (uc *GroupUseCase) CreateGroup(ctx context.Context, ownerID int64, name string, memberIDs []int64) (*Conversation, int, error) {
	if ownerID <= 0 {
		return nil, 0, ErrInvalidParam
	}
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > groupNameMaxLen {
		return nil, 0, ErrInvalidParam
	}

	// 先取 ID 再拼 biz_key：群聊没有天然的"唯一键"，直接用它自己的 ID，
	// 这样一次插入即可完成，避免"先占位再回写"在并发下撞唯一约束。
	id, err := uc.idGen.Next()
	if err != nil {
		return nil, 0, err
	}

	// 先确定成员再去重，这样 member_count 可以在插入时一次写对，
	// 避免"先建会话、再回写人数"这一步在并发下产生不一致。
	members := []*ConversationMember{
		{UserID: ownerID, Role: MemberRoleOwner},
	}
	seen := map[int64]struct{}{ownerID: {}}
	for _, uid := range memberIDs {
		if uid <= 0 {
			continue
		}
		if _, dup := seen[uid]; dup {
			continue
		}
		seen[uid] = struct{}{}
		members = append(members, &ConversationMember{UserID: uid, Role: MemberRoleMember})
	}
	if len(members) > groupMaxMembers {
		return nil, 0, ErrInvalidParam
	}

	conv := &Conversation{
		ID:          id,
		Type:        ConversationTypeGroup,
		BizKey:      GroupBizKey(id),
		Name:        name,
		Status:      ConversationStatusNormal,
		OwnerID:     ownerID,
		MemberCount: int32(len(members)),
		CreatedAt:   time.Now(),
	}

	if err := uc.convRepo.Create(ctx, conv, members); err != nil {
		return nil, 0, err
	}

	uc.log.Infow("msg", "群聊已创建", "conversationId", conv.ID, "owner", ownerID, "members", len(members))
	return conv, len(members) - 1, nil
}

// AddMembers 向群聊添加成员。
//
// 操作者必须是群成员；已在群内的用户会被跳过，返回实际新增人数，因此重复调用是幂等的。
func (uc *GroupUseCase) AddMembers(ctx context.Context, conversationID, operatorID int64, userIDs []int64) (int, error) {
	if conversationID <= 0 || len(userIDs) == 0 {
		return 0, ErrInvalidParam
	}
	if _, err := uc.mustBeMember(ctx, conversationID, operatorID); err != nil {
		return 0, err
	}

	existing, err := uc.convRepo.ListMembers(ctx, conversationID)
	if err != nil {
		return 0, err
	}
	if len(existing)+len(userIDs) > groupMaxMembers {
		return 0, ErrInvalidParam
	}

	seen := make(map[int64]struct{}, len(existing))
	for _, m := range existing {
		seen[m.UserID] = struct{}{}
	}

	members := make([]*ConversationMember, 0, len(userIDs))
	for _, uid := range userIDs {
		if uid <= 0 {
			continue
		}
		if _, dup := seen[uid]; dup {
			continue
		}
		seen[uid] = struct{}{}
		members = append(members, &ConversationMember{
			UserID: uid,
			Role:   MemberRoleMember,
		})
	}
	if len(members) == 0 {
		return 0, nil
	}

	added, err := uc.convRepo.AddMembers(ctx, conversationID, members)
	if err != nil {
		return 0, err
	}

	uc.log.Infow("msg", "群成员已新增", "conversationId", conversationID, "operator", operatorID, "added", added)
	return added, nil
}

// RemoveMember 将成员移出群聊，仅群主与管理员可操作。
func (uc *GroupUseCase) RemoveMember(ctx context.Context, conversationID, operatorID, targetID int64) error {
	operator, err := uc.mustBeMember(ctx, conversationID, operatorID)
	if err != nil {
		return err
	}
	if operator.Role != MemberRoleOwner && operator.Role != MemberRoleAdmin {
		return ErrNoPermission
	}
	if operatorID == targetID {
		return ErrInvalidParam
	}

	target, err := uc.convRepo.FindMember(ctx, conversationID, targetID)
	if err != nil {
		return err
	}
	if target == nil {
		return ErrNotConversationMember
	}
	// 群主不能被移除，否则群会失去所有者
	if target.Role == MemberRoleOwner {
		return ErrCannotRemoveOwner
	}

	return uc.convRepo.RemoveMember(ctx, conversationID, targetID)
}

// QuitGroup 退出群聊。
//
// 群主不能直接退群：群会因此没有所有者。需要先转让群主（当前版本未提供该能力），
// 这一限制是刻意的，避免产生无主群聊。
func (uc *GroupUseCase) QuitGroup(ctx context.Context, conversationID, userID int64) error {
	member, err := uc.mustBeMember(ctx, conversationID, userID)
	if err != nil {
		return err
	}
	if member.Role == MemberRoleOwner {
		return ErrGroupOwnerCannotQuit
	}
	return uc.convRepo.RemoveMember(ctx, conversationID, userID)
}

// UpdateGroupInfo 更新群资料，或更新自己在群内的昵称。
func (uc *GroupUseCase) UpdateGroupInfo(ctx context.Context, conversationID, operatorID int64, name, avatarURL, aliasName string) error {
	member, err := uc.mustBeMember(ctx, conversationID, operatorID)
	if err != nil {
		return err
	}

	// 备注名是成员维度的属性，任何人都可以改自己的
	if alias := strings.TrimSpace(aliasName); alias != "" {
		if len([]rune(alias)) > 32 {
			return ErrInvalidParam
		}
		if err := uc.convRepo.UpdateMemberAlias(ctx, conversationID, operatorID, alias); err != nil {
			return err
		}
	}

	name = strings.TrimSpace(name)
	avatarURL = strings.TrimSpace(avatarURL)
	if name == "" && avatarURL == "" {
		return nil
	}

	// 群名与群头像属于群维度，只有群主与管理员可以改
	if member.Role != MemberRoleOwner && member.Role != MemberRoleAdmin {
		return ErrNoPermission
	}
	if name != "" && len([]rune(name)) > groupNameMaxLen {
		return ErrInvalidParam
	}

	conv, err := uc.convRepo.FindByID(ctx, conversationID)
	if err != nil {
		return err
	}
	if name != "" {
		conv.Name = name
	}
	if avatarURL != "" {
		conv.AvatarURL = avatarURL
	}
	return uc.convRepo.Update(ctx, conv)
}

// ListMembers 返回群成员列表，含昵称与头像。
func (uc *GroupUseCase) ListMembers(ctx context.Context, conversationID, viewerID int64, withProfile bool) ([]*GroupMemberDetail, error) {
	if conversationID <= 0 {
		return nil, ErrInvalidParam
	}
	if _, err := uc.mustBeMember(ctx, conversationID, viewerID); err != nil {
		return nil, err
	}

	members, err := uc.convRepo.ListMembers(ctx, conversationID)
	if err != nil {
		return nil, err
	}

	briefs := map[int64]*UserBrief{}
	if withProfile && len(members) > 0 {
		ids := make([]int64, 0, len(members))
		for _, m := range members {
			ids = append(ids, m.UserID)
		}
		if briefs, err = uc.users.BatchGetBriefs(ctx, ids); err != nil {
			// 资料缺失不影响成员列表本身，降级为空资料
			uc.log.Errorw("msg", "获取群成员资料失败，降级展示", "err", err, "conversationId", conversationID)
			briefs = map[int64]*UserBrief{}
		}
	}

	details := make([]*GroupMemberDetail, 0, len(members))
	for _, m := range members {
		details = append(details, &GroupMemberDetail{Member: m, Brief: briefs[m.UserID]})
	}
	return details, nil
}

// ListMemberIDs 返回群成员的用户 ID 列表，对应旧接口的"群内用户列表"。
func (uc *GroupUseCase) ListMemberIDs(ctx context.Context, conversationID, viewerID int64) ([]int64, error) {
	details, err := uc.ListMembers(ctx, conversationID, viewerID, false)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(details))
	for _, d := range details {
		ids = append(ids, d.Member.UserID)
	}
	return ids, nil
}

// mustBeMember 校验用户是会话成员，并返回其成员信息。
func (uc *GroupUseCase) mustBeMember(ctx context.Context, conversationID, userID int64) (*ConversationMember, error) {
	member, err := uc.convRepo.FindMember(ctx, conversationID, userID)
	if err != nil {
		return nil, err
	}
	if member == nil {
		return nil, ErrNotConversationMember
	}
	return member, nil
}
