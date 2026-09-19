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
		uc.log.Warnw("msg", "群名不合法",
			"owner", ownerID, "nameLen", len([]rune(name)), "max", groupNameMaxLen)
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
		uc.log.Warnw("msg", "建群时成员数超出上限",
			"owner", ownerID, "members", len(members), "max", groupMaxMembers)
		return nil, 0, ErrGroupMemberLimitExceeded
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
// 操作者必须是群成员且具备管理权限；已在群内的用户会被跳过，
// 返回实际新增人数，因此重复调用是幂等的。
//
// 权限收紧到「群主 / 管理员」而非「任意成员」：拉人会把被拉者的名字
// 暴露给全群，并让其收到后续消息，属于对他人有影响的动作。
// 若任何成员都能拉人，一个普通成员就能把无关的人塞进企业群。
func (uc *GroupUseCase) AddMembers(ctx context.Context, conversationID, operatorID int64, userIDs []int64) (int, error) {
	if conversationID <= 0 || len(userIDs) == 0 {
		return 0, ErrInvalidParam
	}
	operator, err := uc.mustBeMember(ctx, conversationID, operatorID)
	if err != nil {
		return 0, err
	}
	if operator.Role != MemberRoleOwner && operator.Role != MemberRoleAdmin {
		uc.log.Warnw("msg", "无权限拉人入群",
			"conversationId", conversationID, "operator", operatorID, "role", operator.Role)
		return 0, ErrNoPermission
	}

	existing, err := uc.convRepo.ListMembers(ctx, conversationID)
	if err != nil {
		return 0, err
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

	// 上限校验放在去重之后：用「现有 + 本次真实新增」判断，
	// 否则重复传入已在群内的 ID 会被误判为超限。
	if len(existing)+len(members) > groupMaxMembers {
		uc.log.Warnw("msg", "群成员数将超出上限",
			"conversationId", conversationID,
			"existing", len(existing), "adding", len(members), "max", groupMaxMembers)
		return 0, ErrGroupMemberLimitExceeded
	}

	added, err := uc.convRepo.AddMembers(ctx, conversationID, members)
	if err != nil {
		return 0, err
	}

	uc.log.Infow("msg", "群成员已新增",
		"conversationId", conversationID, "operator", operatorID, "added", added)
	return added, nil
}

// RemoveMember 将成员移出群聊，仅群主与管理员可操作。
func (uc *GroupUseCase) RemoveMember(ctx context.Context, conversationID, operatorID, targetID int64) error {
	operator, err := uc.mustBeMember(ctx, conversationID, operatorID)
	if err != nil {
		return err
	}
	if operator.Role != MemberRoleOwner && operator.Role != MemberRoleAdmin {
		uc.log.Warnw("msg", "无权限移除群成员",
			"conversationId", conversationID, "operator", operatorID, "role", operator.Role)
		return ErrNoPermission
	}
	if operatorID == targetID {
		uc.log.Warnw("msg", "尝试移除自己",
			"conversationId", conversationID, "operator", operatorID)
		return ErrInvalidParam
	}

	target, err := uc.convRepo.FindMember(ctx, conversationID, targetID)
	if err != nil {
		uc.log.Errorw("msg", "查询待移除成员失败",
			"conversationId", conversationID, "targetId", targetID, "err", err)
		return err
	}
	if target == nil {
		uc.log.Warnw("msg", "待移除的对象不是群成员",
			"conversationId", conversationID, "targetId", targetID)
		return ErrNotConversationMember
	}
	// 群主不能被移除，否则群会失去所有者
	if target.Role == MemberRoleOwner {
		uc.log.Warnw("msg", "尝试移除群主被拒",
			"conversationId", conversationID, "operator", operatorID, "targetId", targetID)
		return ErrCannotRemoveOwner
	}

	if err := uc.convRepo.RemoveMember(ctx, conversationID, targetID); err != nil {
		uc.log.Errorw("msg", "移除群成员失败",
			"conversationId", conversationID, "operator", operatorID,
			"targetId", targetID, "err", err)
		return err
	}
	uc.log.Infow("msg", "群成员已被移除",
		"conversationId", conversationID, "operator", operatorID, "targetId", targetID)
	return nil
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
		uc.log.Warnw("msg", "群主尝试退群被拒",
			"conversationId", conversationID, "uid", userID)
		return ErrGroupOwnerCannotQuit
	}
	if err := uc.convRepo.RemoveMember(ctx, conversationID, userID); err != nil {
		uc.log.Errorw("msg", "退出群聊失败",
			"conversationId", conversationID, "uid", userID, "err", err)
		return err
	}
	uc.log.Infow("msg", "用户已退出群聊", "conversationId", conversationID, "uid", userID)
	return nil
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
			uc.log.Warnw("msg", "群内昵称超长",
				"conversationId", conversationID, "uid", operatorID,
				"len", len([]rune(alias)), "max", 32)
			return ErrInvalidParam
		}
		if err := uc.convRepo.UpdateMemberAlias(ctx, conversationID, operatorID, alias); err != nil {
			uc.log.Errorw("msg", "更新群内昵称失败",
				"conversationId", conversationID, "uid", operatorID, "err", err)
			return err
		}
		uc.log.Infow("msg", "群内昵称已更新",
			"conversationId", conversationID, "uid", operatorID)
	}

	name = strings.TrimSpace(name)
	avatarURL = strings.TrimSpace(avatarURL)
	if name == "" && avatarURL == "" {
		return nil
	}

	// 群名与群头像属于群维度，只有群主与管理员可以改
	if member.Role != MemberRoleOwner && member.Role != MemberRoleAdmin {
		uc.log.Warnw("msg", "无权限修改群资料",
			"conversationId", conversationID, "operator", operatorID, "role", member.Role)
		return ErrNoPermission
	}
	if name != "" && len([]rune(name)) > groupNameMaxLen {
		uc.log.Warnw("msg", "群名超长",
			"conversationId", conversationID, "operator", operatorID,
			"len", len([]rune(name)), "max", groupNameMaxLen)
		return ErrInvalidParam
	}

	conv, err := uc.convRepo.FindByID(ctx, conversationID)
	if err != nil {
		uc.log.Errorw("msg", "修改群资料时查询会话失败",
			"conversationId", conversationID, "err", err)
		return err
	}
	if name != "" {
		conv.Name = name
	}
	if avatarURL != "" {
		conv.AvatarURL = avatarURL
	}
	if err := uc.convRepo.Update(ctx, conv); err != nil {
		uc.log.Errorw("msg", "更新群资料失败",
			"conversationId", conversationID, "operator", operatorID, "err", err)
		return err
	}
	uc.log.Infow("msg", "群资料已更新",
		"conversationId", conversationID, "operator", operatorID,
		"name", conv.Name)
	return nil
}

// ListMembers 返回群成员列表，含昵称与头像。
func (uc *GroupUseCase) ListMembers(ctx context.Context, conversationID, viewerID int64, withProfile bool) ([]*GroupMemberDetail, error) {
	if conversationID <= 0 {
		uc.log.Warnw("msg", "查询群成员列表参数非法", "conversationId", conversationID)
		return nil, ErrInvalidParam
	}
	if _, err := uc.mustBeMember(ctx, conversationID, viewerID); err != nil {
		return nil, err
	}

	members, err := uc.convRepo.ListMembers(ctx, conversationID)
	if err != nil {
		uc.log.Errorw("msg", "查询群成员列表失败",
			"conversationId", conversationID, "err", err)
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
	// Debug 级别：群成员列表会被频繁打开，且成员数可能较大
	uc.log.Debugw("msg", "群成员列表已返回",
		"conversationId", conversationID, "viewer", viewerID,
		"withProfile", withProfile, "count", len(details))
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
//
// 与 ConversationUseCase.mustBeMember 一致：把「谁被拒了」记成 Warn，
// 偶发是客户端 bug，突增则可能是越权探测。
func (uc *GroupUseCase) mustBeMember(ctx context.Context, conversationID, userID int64) (*ConversationMember, error) {
	member, err := uc.convRepo.FindMember(ctx, conversationID, userID)
	if err != nil {
		uc.log.Errorw("msg", "查询群成员失败",
			"conversationId", conversationID, "uid", userID, "err", err)
		return nil, err
	}
	if member == nil {
		uc.log.Warnw("msg", "非群成员访问被拒",
			"conversationId", conversationID, "uid", userID)
		return nil, ErrNotConversationMember
	}
	return member, nil
}
