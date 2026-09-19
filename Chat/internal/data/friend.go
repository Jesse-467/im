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
var _ biz.FriendRepo = (*friendRepo)(nil)

// friendRepo 是 biz.FriendRepo 的 GORM 实现。
type friendRepo struct{ data *Data }

// NewFriendRepo 构造好友仓储。返回接口类型，便于 Wire 完成依赖绑定。
func NewFriendRepo(d *Data) biz.FriendRepo { return &friendRepo{data: d} }

// CreateRelation 在单个事务内双向写入好友关系。
//
// 两行必须同生共死：好友关系是双向语义，只成功一行会留下「A 认为 B 是好友、
// B 不认识 A」的单边数据，这类脏数据无法靠重试修复，只能人工干预。
// ON CONFLICT DO NOTHING 让重复调用（重复同意申请）保持幂等，
// remark 只写在发起方那一行，因为备注属于「谁对谁的备注」。
func (r *friendRepo) CreateRelation(ctx context.Context, uidA, uidB int64, remark string) error {
	rows := []*friendRelationModel{
		{UserID: uidA, FriendID: uidB, Remark: remark, Status: biz.FriendStatusNormal},
		{UserID: uidB, FriendID: uidA, Remark: "", Status: biz.FriendStatusNormal},
	}

	err := r.data.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
	})
	if err != nil {
		return fmt.Errorf("data: 创建好友关系失败: %w", err)
	}
	return nil
}

// AreFriends 判断双方是否为好友。
//
// 必须同时满足两个条件：双向两行都存在，且两行的 status 都是正常。
// 任一方拉黑即视为非好友——否则被拉黑者仍能发消息，拉黑就形同虚设。
// 这里一次性把两行查出来判断，而不是分两次查询，避免两次查询之间状态发生变化
// 导致「A 正常、B 拉黑」被误判为好友。
func (r *friendRepo) AreFriends(ctx context.Context, uidA, uidB int64) (bool, error) {
	var rows []friendRelationModel
	err := r.data.db.WithContext(ctx).
		Where("(user_id = ? AND friend_id = ?) OR (user_id = ? AND friend_id = ?)", uidA, uidB, uidB, uidA).
		Find(&rows).Error
	if err != nil {
		return false, fmt.Errorf("data: 查询好友关系失败: %w", err)
	}

	// 双向各一行，少于两行说明关系不完整（例如只删掉了一边）
	if len(rows) < 2 {
		return false, nil
	}
	for i := range rows {
		if rows[i].Status != biz.FriendStatusNormal {
			return false, nil
		}
	}
	return true, nil
}

// ListRelations 返回用户的好友关系列表，已拉黑的关系不返回。
//
// 只查 user_id 单列：双向存行的设计让该查询退化为一次索引扫描，
// 不需要 OR friend_id = ?（后者无法有效利用索引，是双向存两行的主要收益）。
func (r *friendRepo) ListRelations(ctx context.Context, userID int64) ([]*biz.FriendRelation, error) {
	var rows []friendRelationModel
	err := r.data.db.WithContext(ctx).
		Where("user_id = ? AND status = ?", userID, biz.FriendStatusNormal).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("data: 查询好友列表失败: %w", err)
	}

	relations := make([]*biz.FriendRelation, 0, len(rows))
	for i := range rows {
		relations = append(relations, toBizFriendRelation(&rows[i]))
	}
	return relations, nil
}

// DeleteRelation 删除双向好友关系。
//
// 一条 DELETE 覆盖两个方向：单条语句本身就是原子的（隐式事务），
// 不会出现只删掉一边的中间态，因此无需显式开启事务（少一次事务提交开销）。
// 关系不存在时同样返回成功，保证重复删除的幂等。
func (r *friendRepo) DeleteRelation(ctx context.Context, userID, friendID int64) error {
	err := r.data.db.WithContext(ctx).
		Where("(user_id = ? AND friend_id = ?) OR (user_id = ? AND friend_id = ?)", userID, friendID, friendID, userID).
		Delete(&friendRelationModel{}).Error
	if err != nil {
		return fmt.Errorf("data: 删除好友关系失败: %w", err)
	}
	return nil
}

// SetBlocked 设置（或取消）拉黑状态，只作用于 user 自己那一行。
//
// 拉黑用 upsert 实现「不存在就补建、存在就置为拉黑」：拆分「先查再插」在并发下
// 会撞 (user_id, friend_id) 唯一约束，而一条 ON CONFLICT DO UPDATE 既原子又能
// 只改 status、不覆盖已存在的 remark。
// 取消拉黑则只更新已存在的行——若关系不存在也补建，就等同于凭空造出一条好友关系。
func (r *friendRepo) SetBlocked(ctx context.Context, userID, targetID int64, blocked bool) error {
	if !blocked {
		err := r.data.db.WithContext(ctx).
			Model(&friendRelationModel{}).
			Where("user_id = ? AND friend_id = ?", userID, targetID).
			Update("status", biz.FriendStatusNormal).Error
		if err != nil {
			return fmt.Errorf("data: 取消拉黑失败: %w", err)
		}
		return nil
	}

	err := r.data.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}, {Name: "friend_id"}},
			DoUpdates: clause.Assignments(map[string]any{"status": biz.FriendStatusBlocked}),
		}).
		Create(&friendRelationModel{
			UserID:   userID,
			FriendID: targetID,
			Status:   biz.FriendStatusBlocked,
		}).Error
	if err != nil {
		return fmt.Errorf("data: 拉黑用户失败: %w", err)
	}
	return nil
}

// CreateRequest 创建好友申请。
func (r *friendRepo) CreateRequest(ctx context.Context, req *biz.FriendRequest) error {
	m := fromBizFriendRequest(req)

	err := r.data.db.WithContext(ctx).Create(m).Error
	if err != nil {
		// (from_uid, to_uid, status) 唯一约束用来兜住「连点两次申请」：并发下只有一个
		// 请求能插入成功，另一个命中唯一冲突，其语义恰好等价于「已有待处理申请」。
		if isDuplicateKey(err) {
			return biz.ErrFriendRequestPending
		}
		return fmt.Errorf("data: 创建好友申请失败: %w", err)
	}

	req.ID = m.ID
	req.CreatedAt = m.CreatedAt
	return nil
}

// FindRequestByID 按主键查询好友申请。
func (r *friendRepo) FindRequestByID(ctx context.Context, id int64) (*biz.FriendRequest, error) {
	var m friendRequestModel
	err := r.data.db.WithContext(ctx).Where("id = ?", id).First(&m).Error
	if err != nil {
		return nil, normalize(err, biz.ErrFriendRequestNotFound, "按 ID 查询好友申请")
	}
	return toBizFriendRequest(&m), nil
}

// FindPendingRequest 查询 from -> to 的待处理申请，不存在返回 (nil, nil)。
//
// 返回 nil 而不是错误，是因为「没有待处理申请」是发起申请的必经正常路径，
// 若用错误表达，调用方就得区分「业务上没有」和「查询失败」两类错误。
func (r *friendRepo) FindPendingRequest(ctx context.Context, fromUID, toUID int64) (*biz.FriendRequest, error) {
	var m friendRequestModel
	err := r.data.db.WithContext(ctx).
		Where("from_uid = ? AND to_uid = ? AND status = ?", fromUID, toUID, biz.FriendRequestPending).
		// 唯一约束保证最多一行，排序只是为了在历史脏数据下取到最新那条
		Order("id DESC").
		First(&m).Error
	if err != nil {
		if isRecordNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("data: 查询待处理好友申请失败: %w", err)
	}
	return toBizFriendRequest(&m), nil
}

// UpdateRequestStatus 推进申请状态，返回本次调用是否真的改动了状态。
//
// 把「检查当前是否待处理」合并进 UPDATE 的 WHERE 条件，使「判断 + 更新」成为一次
// 原子操作：并发重复提交同一条申请时，数据库只会让其中一个事务更新到行，
// 其余返回 0 行受影响即 false。若写成先查后改，两个请求都会读到「待处理」，
// 结果是重复建立好友关系与会话。
func (r *friendRepo) UpdateRequestStatus(ctx context.Context, id int64, status int32, handledAt time.Time) (bool, error) {
	res := r.data.db.WithContext(ctx).
		Model(&friendRequestModel{}).
		Where("id = ? AND status = ?", id, biz.FriendRequestPending).
		Updates(map[string]any{
			"status":     status,
			"handled_at": nullableTime(handledAt),
		})
	if res.Error != nil {
		return false, fmt.Errorf("data: 更新好友申请状态失败: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// ListRequests 查询用户收到的好友申请。
//
// status 为负数表示不过滤状态：用「负数」而不是 0 表示不过滤，是因为 0 本身就是
// 「待处理」这一合法状态值，用 0 表达不过滤会与业务语义冲突。
func (r *friendRepo) ListRequests(ctx context.Context, userID int64, status int32, limit int) ([]*biz.FriendRequest, error) {
	q := r.data.db.WithContext(ctx).Where("to_uid = ?", userID)
	if status >= 0 {
		q = q.Where("status = ?", status)
	}

	var rows []friendRequestModel
	if err := q.Order("created_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("data: 查询好友申请列表失败: %w", err)
	}

	requests := make([]*biz.FriendRequest, 0, len(rows))
	for i := range rows {
		requests = append(requests, toBizFriendRequest(&rows[i]))
	}
	return requests, nil
}
