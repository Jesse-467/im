package data

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Jesse-467/im/Account/internal/biz"
)

// 编译期断言：仓储实现必须满足业务层声明的接口。
var _ biz.TokenRepo = (*tokenRepo)(nil)

// tokenModel 是 account_token 表的 ORM 映射。
//
// 表名与结构见 migrations/0002_token.up.sql。这里刻意不存令牌原文，
// 只存 jti —— 即使数据库被读取也无法据此伪造令牌。
type tokenModel struct {
	ID         int64      `gorm:"column:id;primaryKey;autoIncrement"`
	UserID     int64      `gorm:"column:user_id;not null;index"`
	JTI        string     `gorm:"column:jti;size:64;not null;uniqueIndex"`
	DeviceID   string     `gorm:"column:device_id;size:128;not null;default:''"`
	Platform   string     `gorm:"column:platform;size:32;not null;default:'unknown'"`
	ExpireAt   time.Time  `gorm:"column:expire_at;not null"`
	RevokedAt  *time.Time `gorm:"column:revoked_at"`
	RevokedBy  string     `gorm:"column:revoked_by;size:32;not null;default:''"`
	LastSeenAt time.Time  `gorm:"column:last_seen_at;not null"`
	CreatedAt  time.Time  `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt  time.Time  `gorm:"column:updated_at;autoUpdateTime"`
}

// TableName 指定表名。
func (tokenModel) TableName() string { return "account_token" }

// tokenRepo 是 biz.TokenRepo 的 GORM 实现。
type tokenRepo struct{ data *Data }

// NewTokenRepo 构造令牌仓储。
func NewTokenRepo(d *Data) biz.TokenRepo { return &tokenRepo{data: d} }

// Save 写入或更新一条令牌记录。
//
// 用 ON CONFLICT (user_id, device_id) DO UPDATE 而不是先查后写：
//   - 同一设备重新登录时复用同一行，因此设备数上限不会被自己的重复登录占满；
//   - 先查后写在并发下会同时判定「不存在」而双双插入，撞唯一约束报错，
//     而 DO UPDATE 无需重试即可安全并发。
//
// 冲突时会把 revoked_at 清空：那台设备重新登录成功，就代表它重新获得授权，
// 旧行上残留的吊销标记必须一并清除，否则新令牌刚签发就处于「已吊销」状态。
func (r *tokenRepo) Save(ctx context.Context, token *biz.AuthToken) error {
	m := &tokenModel{
		UserID:     token.UserID,
		JTI:        token.JTI,
		DeviceID:   token.DeviceID,
		Platform:   token.Platform,
		ExpireAt:   token.ExpireAt,
		LastSeenAt: time.Now(),
		CreatedAt:  token.CreatedAt,
	}

	err := r.data.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "user_id"}, {Name: "device_id"}},
			DoUpdates: clause.Assignments(map[string]any{
				"jti":          m.JTI,
				"platform":     m.Platform,
				"expire_at":    m.ExpireAt,
				"revoked_at":   nil,
				"revoked_by":   "",
				"last_seen_at": time.Now(),
				"updated_at":   time.Now(),
			}),
		}).
		Create(m).Error
	if err != nil {
		return fmt.Errorf("data: 保存令牌失败: %w", err)
	}

	token.ID = m.ID
	return nil
}

// FindByJTI 按 jti 查询令牌，不存在时返回 (nil, nil)。
func (r *tokenRepo) FindByJTI(ctx context.Context, jti string) (*biz.AuthToken, error) {
	var m tokenModel
	err := r.data.db.WithContext(ctx).Where("jti = ?", jti).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 未命中不是错误：调用方用 nil 表达「该令牌不存在」这一正常分支
			return nil, nil
		}
		return nil, fmt.Errorf("data: 按 jti 查询令牌失败: %w", err)
	}
	return toBizToken(&m), nil
}

// FindByDevice 按 device_key 查询令牌。
//
// device_key 由 biz.DeviceKey 计算（有 deviceID 用 "d:<id>"，否则用 "j:<jti>"），
// 因此这里要按同样的规则在 device_id 与 jti 两列上分别匹配。
// 只取最新的那一行：同一设备若因历史原因留下多行（如 deviceID 后补），
// 踢人时应当针对当前生效的那条。
func (r *tokenRepo) FindByDevice(ctx context.Context, userID int64, deviceKey string) (*biz.AuthToken, error) {
	var m tokenModel
	q := r.data.db.WithContext(ctx).Model(&tokenModel{}).Where("user_id = ?", userID)

	switch {
	case len(deviceKey) > 2 && deviceKey[:2] == "d:":
		q = q.Where("device_id = ?", deviceKey[2:])
	case len(deviceKey) > 2 && deviceKey[:2] == "j:":
		q = q.Where("jti = ?", deviceKey[2:])
	default:
		// 非法标识：直接返回空，避免把整个用户的令牌都查出来
		return nil, nil
	}

	err := q.Order("created_at DESC").First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("data: 按设备查询令牌失败: %w", err)
	}
	return toBizToken(&m), nil
}

// Revoke 吊销一条令牌。
//
// 条件里限定 revoked_at IS NULL：重复吊销不会覆盖首次的吊销原因与时间，
// 这样审计时看到的永远是「最初是哪一次操作让它失效的」。
func (r *tokenRepo) Revoke(ctx context.Context, jti, reason string, revokedAt time.Time) error {
	err := r.data.db.WithContext(ctx).
		Model(&tokenModel{}).
		Where("jti = ? AND revoked_at IS NULL", jti).
		Updates(map[string]any{
			"revoked_at": revokedAt,
			"revoked_by": reason,
			"updated_at": time.Now(),
		}).Error
	if err != nil {
		return fmt.Errorf("data: 吊销令牌失败: %w", err)
	}
	return nil
}

// RevokeAllByUser 吊销某用户的全部有效令牌，返回受影响条数。
func (r *tokenRepo) RevokeAllByUser(ctx context.Context, userID int64, reason string, revokedAt time.Time) (int, error) {
	res := r.data.db.WithContext(ctx).
		Model(&tokenModel{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Updates(map[string]any{
			"revoked_at": revokedAt,
			"revoked_by": reason,
			"updated_at": time.Now(),
		})
	if res.Error != nil {
		return 0, fmt.Errorf("data: 吊销用户全部令牌失败: %w", res.Error)
	}
	return int(res.RowsAffected), nil
}

// Touch 刷新令牌的最后活跃时间。
//
// 用单列 Update 而非 Updates：这里只关心 last_seen_at，
// 顺带刷新 updated_at 会让「令牌行最近被改动过」的语义变得含糊。
func (r *tokenRepo) Touch(ctx context.Context, jti string, seenAt time.Time) error {
	err := r.data.db.WithContext(ctx).
		Model(&tokenModel{}).
		Where("jti = ?", jti).
		Update("last_seen_at", seenAt).Error
	if err != nil {
		return fmt.Errorf("data: 刷新令牌活跃时间失败: %w", err)
	}
	return nil
}

// toBizToken 把存储模型转换为业务实体。
func toBizToken(m *tokenModel) *biz.AuthToken {
	return &biz.AuthToken{
		ID:         m.ID,
		UserID:     m.UserID,
		JTI:        m.JTI,
		DeviceID:   m.DeviceID,
		Platform:   m.Platform,
		ExpireAt:   m.ExpireAt,
		RevokedAt:  m.RevokedAt,
		RevokedBy:  m.RevokedBy,
		LastSeenAt: m.LastSeenAt,
		CreatedAt:  m.CreatedAt,
	}
}
