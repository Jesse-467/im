package data

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Jesse-467/im/Chat/internal/biz"
)

// 编译期断言：仓储实现必须满足业务层声明的接口。
var _ biz.ConversationRepo = (*conversationRepo)(nil)

// conversationRepo 是 biz.ConversationRepo 的 GORM 实现。
type conversationRepo struct{ data *Data }

// NewConversationRepo 构造会话仓储。返回接口类型，便于 Wire 完成依赖绑定。
func NewConversationRepo(d *Data) biz.ConversationRepo { return &conversationRepo{data: d} }

// Create 在单个事务内创建会话与全部成员。
//
// 两张表必须一起成功：会话是「消息容器」，成员决定谁能看到它，
// 只写入会话会让这个容器对任何人都不可见且无法被列表查询发现，
// 属于重试也无法自愈的孤儿数据（会话 ID 由调用方生成，重复插入会直接冲突）。
func (r *conversationRepo) Create(ctx context.Context, conv *biz.Conversation, members []*biz.ConversationMember) error {
	m := fromBizConversation(conv)

	err := r.data.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(m).Error; err != nil {
			return err
		}

		if len(members) == 0 {
			return nil
		}
		rows := make([]*conversationMemberModel, 0, len(members))
		for _, member := range members {
			row := fromBizMember(member)
			// 以本次插入的会话 ID 为准，避免调用方漏填 ConversationID
			// 把成员挂到一个不存在（或别人的）会话上。
			row.ConversationID = conv.ID
			rows = append(rows, row)
		}
		return tx.Create(&rows).Error
	})
	if err != nil {
		return fmt.Errorf("data: 创建会话失败: %w", err)
	}

	// 回填由数据库/GORM 维护的字段，使调用方拿到完整的实体
	conv.CreatedAt = m.CreatedAt
	return nil
}

// FindByID 按主键查询会话。
func (r *conversationRepo) FindByID(ctx context.Context, id int64) (*biz.Conversation, error) {
	var m conversationModel
	err := r.data.db.WithContext(ctx).Where("id = ?", id).First(&m).Error
	if err != nil {
		return nil, normalize(err, biz.ErrConversationNotFound, "按 ID 查询会话")
	}
	return toBizConversation(&m), nil
}

// FindByBizKey 按 (type, biz_key) 查询会话。
//
// 单聊靠它复用已有会话；唯一约束与查询条件完全对应，
// 因此这里查到的就是唯一命中的那一行。
func (r *conversationRepo) FindByBizKey(ctx context.Context, convType int32, bizKey string) (*biz.Conversation, error) {
	var m conversationModel
	err := r.data.db.WithContext(ctx).
		Where("type = ? AND biz_key = ?", convType, bizKey).
		First(&m).Error
	if err != nil {
		return nil, normalize(err, biz.ErrConversationNotFound, "按业务键查询会话")
	}
	return toBizConversation(&m), nil
}

// ListByUser 返回用户参与的全部会话及其成员行，按会话最近活跃时间倒序。
//
// 会话与成员一次联表查出：成员行里的已读位点是未读数计算的必要输入，
// 若拆成两次查询，中间出现的新消息会让两者不一致。排序交给数据库完成，
// 避免把全部会话捞进内存再排序。
func (r *conversationRepo) ListByUser(ctx context.Context, userID int64) ([]*biz.UserConversation, error) {
	var rows []userConversationRow
	err := r.data.db.WithContext(ctx).
		Table("conversation AS c").
		Select("c.id, c.type, c.biz_key, c.name, c.avatar_url, c.status, c.owner_id, c.max_seq, c.created_at, "+
			"m.alias_name, m.role, m.last_read_seq, m.unread_count, m.joined_at").
		Joins("JOIN conversation_member AS m ON m.conversation_id = c.id").
		// 已离开的成员仍保留成员行（用于历史追溯），但不应再出现在会话列表里
		Where("m.user_id = ? AND m.left_at IS NULL", userID).
		// updated_at 代表会话最近活跃时间：新消息推进 max_seq 时会一并刷新它
		Order("c.updated_at DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("data: 查询用户会话列表失败: %w", err)
	}

	relations := make([]*biz.UserConversation, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		relations = append(relations, &biz.UserConversation{
			Conversation: &biz.Conversation{
				ID:        row.ID,
				Type:      row.Type,
				BizKey:    row.BizKey,
				Name:      row.Name,
				AvatarURL: row.AvatarURL,
				Status:    row.Status,
				OwnerID:   row.OwnerID,
				MaxSeq:    row.MaxSeq,
				CreatedAt: row.CreatedAt,
			},
			Member: &biz.ConversationMember{
				ConversationID: row.ID,
				UserID:         userID,
				AliasName:      row.AliasName,
				Role:           row.Role,
				LastReadSeq:    row.LastReadSeq,
				JoinedAt:       row.JoinedAt,
			},
		})
	}
	return relations, nil
}

// FindPeerIDs 批量查询单聊会话中的「对方」用户 ID。
//
// 一次查询覆盖全部会话：会话列表是最高频的接口，若按会话逐个查成员，
// 单次请求的 IO 次数会随会话数量线性增长（N+1）。
func (r *conversationRepo) FindPeerIDs(ctx context.Context, conversationIDs []int64, selfID int64) (map[int64]int64, error) {
	peers := make(map[int64]int64, len(conversationIDs))
	if len(conversationIDs) == 0 {
		return peers, nil
	}

	var rows []struct {
		ConversationID int64 `gorm:"column:conversation_id"`
		UserID         int64 `gorm:"column:user_id"`
	}
	err := r.data.db.WithContext(ctx).
		Table(conversationMemberModel{}.TableName()+" AS m").
		Select("m.conversation_id, m.user_id").
		// 限定单聊：群聊成员多于两人，「对方」没有唯一答案；把类型过滤交给数据库，
		// 调用方就不必先查一遍会话类型，也避免把无关的群成员塞进用户资料批量查询。
		Joins("JOIN "+conversationModel{}.TableName()+" AS c ON c.id = m.conversation_id").
		Where("m.conversation_id IN ? AND m.user_id <> ? AND c.type = ?", conversationIDs, selfID, biz.ConversationTypeSingle).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("data: 批量查询会话对方 ID 失败: %w", err)
	}

	for i := range rows {
		// 单聊固定两名成员，排除自己后每个会话恰好一行
		peers[rows[i].ConversationID] = rows[i].UserID
	}
	return peers, nil
}

// AddMembers 批量加入成员，已存在的成员自动跳过。
//
// 用 ON CONFLICT DO NOTHING 让幂等性由数据库唯一约束（conversation_id, user_id）保证：
// 先查后插在并发下仍会撞唯一约束并报错，而 DO NOTHING 无需重试即可安全重复调用，
// 同时 RowsAffected 精确反映本次真实新增的人数。
func (r *conversationRepo) AddMembers(ctx context.Context, conversationID int64, members []*biz.ConversationMember) (int, error) {
	if len(members) == 0 {
		return 0, nil
	}

	rows := make([]*conversationMemberModel, 0, len(members))
	for _, member := range members {
		row := fromBizMember(member)
		row.ConversationID = conversationID
		rows = append(rows, row)
	}

	res := r.data.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			// 重新入群时复用原有成员行：把 left_at 清空表示「又在场了」。
			// 若只做 DoNothing，退过群的人再次入群会因唯一约束而加不进来，
			// 表现为「拉人成功但对方看不到群」这类难查的问题。
			Columns:   []clause.Column{{Name: "conversation_id"}, {Name: "user_id"}},
			DoUpdates: clause.Assignments(map[string]any{"left_at": nil}),
		}).
		Create(&rows)
	if res.Error != nil {
		return 0, fmt.Errorf("data: 批量加入会话成员失败: %w", res.Error)
	}
	// 新增成员后同步推进会话上的冗余计数。
	//
	// 用表达式自增而非「先读再写」：并发拉人时后者会丢更新。
	// 计数只增不减（成员是软删除，行仍在），因此这里不做减法。
	if res.RowsAffected > 0 {
		if err := r.data.db.WithContext(ctx).
			Model(&conversationModel{}).
			Where("id = ?", conversationID).
			Update("member_count", gorm.Expr("member_count + ?", res.RowsAffected)).Error; err != nil {
			return 0, fmt.Errorf("data: 更新会话成员数失败: %w", err)
		}
	}
	return int(res.RowsAffected), nil
}

// RemoveMember 把成员移出会话。
//
// 采用软删除（置 left_at）而非物理删除：
//   - 保留「谁什么时候退的群」这一事实，便于追溯与合规；
//   - 重新入群可直接复用同一行，不会因唯一约束冲突而失败；
//   - 历史消息的发送者引用不会变成悬空。
//
// 重复调用天然幂等：条件里限定 left_at IS NULL，已离开的行不会被再次更新。
func (r *conversationRepo) RemoveMember(ctx context.Context, conversationID, userID int64) error {
	err := r.data.db.WithContext(ctx).
		Model(&conversationMemberModel{}).
		Where("conversation_id = ? AND user_id = ? AND left_at IS NULL", conversationID, userID).
		Updates(map[string]any{"left_at": time.Now(), "unread_count": 0}).Error
	if err != nil {
		return fmt.Errorf("data: 移除会话成员失败: %w", err)
	}
	return nil
}

// FindMember 查询单个成员。
//
// 已离开的成员视为不存在：权限校验依赖本方法，退群后必须立即失去访问权。
// 成员不存在返回 (nil, nil) 而非错误——biz 层用 nil 表达「不是成员」这一正常业务分支
// （例如非成员访问会话应得到 403 而不是 500），若返回错误反而会被当成系统故障。
func (r *conversationRepo) FindMember(ctx context.Context, conversationID, userID int64) (*biz.ConversationMember, error) {
	var m conversationMemberModel
	err := r.data.db.WithContext(ctx).
		Where("conversation_id = ? AND user_id = ? AND left_at IS NULL", conversationID, userID).
		First(&m).Error
	if err != nil {
		if isRecordNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("data: 查询会话成员失败: %w", err)
	}
	return toBizMember(&m), nil
}

// ListMembers 返回会话当前在场的成员。
//
// 角色倒序让群主、管理员排在前面，再按加入时间正序：展示时「谁建的群」一眼可见，
// 同时保证同一群每次返回的顺序稳定（仅按 role 排序时同角色成员顺序不确定）。
func (r *conversationRepo) ListMembers(ctx context.Context, conversationID int64) ([]*biz.ConversationMember, error) {
	var rows []conversationMemberModel
	err := r.data.db.WithContext(ctx).
		Where("conversation_id = ? AND left_at IS NULL", conversationID).
		Order("role DESC, joined_at ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("data: 查询会话成员列表失败: %w", err)
	}

	members := make([]*biz.ConversationMember, 0, len(rows))
	for i := range rows {
		members = append(members, toBizMember(&rows[i]))
	}
	return members, nil
}

// UpdateMemberReadSeq 推进成员的已读位点，并同步扣减未读数。
//
// 条件里带 last_read_seq < ? 而不是直接赋值：客户端可能乱序上报已读，
// 若无条件覆盖，一个迟到的旧位点会把已经推进的位点回退，已读消息重新变成未读。
// 把比较放进 WHERE 后，回退请求只会影响 0 行，天然被忽略。
//
// 未读数这里按「位点推进了多少条」同比例扣减，而不是直接置零：
// 客户端上报的位点可能落后于最新消息，此时仍有未读，置零会丢红点。
func (r *conversationRepo) UpdateMemberReadSeq(ctx context.Context, conversationID, userID, seq int64) error {
	err := r.data.db.WithContext(ctx).
		Model(&conversationMemberModel{}).
		Where("conversation_id = ? AND user_id = ? AND last_read_seq < ?", conversationID, userID, seq).
		Updates(map[string]any{
			"last_read_seq": seq,
			// GREATEST(...,0) 兜底，避免并发下出现负数未读
			"unread_count": gorm.Expr("GREATEST(unread_count - (? - last_read_seq), 0)", seq),
		}).Error
	if err != nil {
		return fmt.Errorf("data: 更新已读位点失败: %w", err)
	}
	return nil
}

// UpdateMemberAlias 更新成员在会话中的备注名。
func (r *conversationRepo) UpdateMemberAlias(ctx context.Context, conversationID, userID int64, alias string) error {
	err := r.data.db.WithContext(ctx).
		Model(&conversationMemberModel{}).
		Where("conversation_id = ? AND user_id = ?", conversationID, userID).
		Update("alias_name", alias).Error
	if err != nil {
		return fmt.Errorf("data: 更新成员备注名失败: %w", err)
	}
	return nil
}

// UpdateMaxSeq 以 CAS 方式推进会话最大序号。
//
// 用 max_seq < ? 作为条件而不是直接赋值：多条消息并发写入时会各自算出新序号，
// 若直接覆盖，序号小的事务后提交就会把 max_seq 拉回去，导致后续消息的序号重复、
// 客户端未读数错乱。CAS 让「落后的更新」直接落空。
// 这里用 Updates 而非 UpdateColumn，是为了让 GORM 自动刷新 updated_at，
// 使会话的「最近活跃时间」随新消息一起前进。
func (r *conversationRepo) UpdateMaxSeq(ctx context.Context, conversationID, seq int64) error {
	err := r.data.db.WithContext(ctx).
		Model(&conversationModel{}).
		Where("id = ? AND max_seq < ?", conversationID, seq).
		Updates(map[string]any{"max_seq": seq}).Error
	if err != nil {
		return fmt.Errorf("data: 更新会话最大序号失败: %w", err)
	}
	return nil
}

// Update 更新会话自身的展示属性。
//
// 刻意不用 Save 全量覆盖：Save 会把内存中的 max_seq 一并写回，
// 而 max_seq 由 UpdateMaxSeq 以 CAS 维护，全量覆盖会让并发写入的新序号丢失。
// 因此这里只更新与并发无关的展示字段。
func (r *conversationRepo) Update(ctx context.Context, conv *biz.Conversation) error {
	err := r.data.db.WithContext(ctx).
		Model(&conversationModel{}).
		Where("id = ?", conv.ID).
		Updates(map[string]any{
			"name":       conv.Name,
			"avatar_url": conv.AvatarURL,
			"status":     conv.Status,
		}).Error
	if err != nil {
		return fmt.Errorf("data: 更新会话失败: %w", err)
	}
	return nil
}
