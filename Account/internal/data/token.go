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

// Save 写入一条令牌记录。
//
// 用 ON CONFLICT (jti) DO UPDATE —— 冲突仲裁者恒为 jti，而不是
// (user_id, device_id)。这里有个 PostgreSQL 的硬约束值得记下来：
//
//	部分唯一索引 (user_id, device_id) WHERE device_id <> ''
//	不能作为 ON CONFLICT 的仲裁索引，除非语句里重复同样的 WHERE 谓词
//	（否则报 SQLSTATE 42P10: no unique or exclusion constraint matching
//	the ON CONFLICT specification）。而 GORM 的 clause.OnConflict 无法
//	生成部分索引的谓词，因此该索引不能用于 upsert。
//
// 用 jti 作仲裁者是安全的：jti 由加密随机数生成，每次登录必然不同，
// 所以正常情况下永远走 INSERT 分支；DO UPDATE 只用于同一个 jti 被重复
// 保存这一种情形（重试、并发），此时覆盖为最新状态即可。
//
// 「同一设备复用同一行」的语义由调用方保证：RegisterDevice 在 Save 之前
// 会先按 deviceKey 清理掉该设备的历史行（见 CleanupDevice），
// 从而同时满足「设备去重」与「upsert 必须用全局唯一索引」这两个约束。
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
			Columns: []clause.Column{{Name: "jti"}},
			DoUpdates: clause.Assignments(map[string]any{
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

// CleanupDevice 删除该设备在此用户下的历史令牌行。
//
// 为什么需要它：同一设备重新登录时，若旧行仍留在表里，会出现
//   - 同一 device_id 对应多条令牌，按设备反查时语义不明确；
//   - 设备数上限被自己的历史登录占满。
//
// 删除而非「标记吊销」：这台设备已经用新令牌重新登录，旧令牌没有任何
// 保留价值，而且保留会让按 deviceKey 反查时同时命中多条。
// 需要审计「何时被顶下线」的场景由 revoked_* 列覆盖（踢人时会写）。
func (r *tokenRepo) CleanupDevice(ctx context.Context, userID int64, deviceID, jti string) error {
	q := r.data.db.WithContext(ctx).
		Where("user_id = ?", userID).
		// 排除本次即将写入的那一条（同 jti 时无需删）
		Where("jti <> ?", jti)

	if deviceID != "" {
		q = q.Where("device_id = ?", deviceID)
	} else {
		// 未上报 deviceId：每次登录都是独立设备，不做清理
		return nil
	}

	if err := q.Delete(&tokenModel{}).Error; err != nil {
		return fmt.Errorf("data: 清理设备历史令牌失败: %w", err)
	}
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
