package data

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/Jesse-467/im/Account/internal/biz"
)

// 编译期断言：仓储实现必须满足业务层声明的接口。
var _ biz.UserRepo = (*userRepo)(nil)

// pgUniqueViolation 是 PostgreSQL 唯一约束冲突的 SQLSTATE 错误码。
const pgUniqueViolation = "23505"

// userModel 是 account_user 表的 ORM 映射。
//
// 表名刻意加 account_ 前缀：user 是 PostgreSQL 的保留字（等价于 current_user 函数），
// 直接作为表名会在不加引号的 SQL 中报语法错误，加上前缀也顺带明确了归属。
//
// 字段与表结构一一对应；业务实体由 biz.User 表达，两者刻意分离，
// 避免表结构变化直接冲击业务层。
type userModel struct {
	ID        int64     `gorm:"column:id;primaryKey;autoIncrement"`
	Email     string    `gorm:"column:email;size:191;not null;uniqueIndex"`
	Password  string    `gorm:"column:password;size:255;not null"`
	Nickname  string    `gorm:"column:nickname;size:64;not null"`
	Gender    int32     `gorm:"column:gender;not null;default:0"`
	AvatarURL string    `gorm:"column:avatar_url;type:text;not null;default:''"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

// TableName 指定表名。
func (userModel) TableName() string { return "account_user" }

// userRepo 是 biz.UserRepo 的 GORM 实现。
type userRepo struct{ data *Data }

// NewUserRepo 构造用户仓储。返回接口类型，便于 Wire 完成依赖绑定。
func NewUserRepo(d *Data) biz.UserRepo { return &userRepo{data: d} }

// FindByID 按主键查询用户。
func (r *userRepo) FindByID(ctx context.Context, id int64) (*biz.User, error) {
	var m userModel
	err := r.data.db.WithContext(ctx).Where("id = ?", id).First(&m).Error
	if err != nil {
		return nil, normalize(err, "按 ID 查询用户")
	}
	return toBiz(&m), nil
}

// FindByEmail 按邮箱查询用户。
func (r *userRepo) FindByEmail(ctx context.Context, email string) (*biz.User, error) {
	var m userModel
	err := r.data.db.WithContext(ctx).Where("email = ?", email).First(&m).Error
	if err != nil {
		return nil, normalize(err, "按邮箱查询用户")
	}
	return toBiz(&m), nil
}

// FindByIDs 批量查询用户，用于一次性填充会话所需的用户资料。
func (r *userRepo) FindByIDs(ctx context.Context, ids []int64) ([]*biz.User, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	var ms []userModel
	if err := r.data.db.WithContext(ctx).Where("id IN ?", ids).Find(&ms).Error; err != nil {
		return nil, fmt.Errorf("data: 批量查询用户失败: %w", err)
	}

	users := make([]*biz.User, 0, len(ms))
	for i := range ms {
		users = append(users, toBiz(&ms[i]))
	}
	return users, nil
}

// Create 创建用户并返回自增主键。
func (r *userRepo) Create(ctx context.Context, u *biz.User) (int64, error) {
	m := fromBiz(u)

	err := r.data.db.WithContext(ctx).Create(m).Error
	if err != nil {
		// 依赖唯一索引兜并发：即使两个请求同时通过了「邮箱是否存在」的前置校验，
		// 也只有一个能写入成功，另一个在这里被拦下。
		if isDuplicateKey(err) {
			return 0, biz.ErrEmailTaken
		}
		return 0, fmt.Errorf("data: 创建用户失败: %w", err)
	}

	u.CreatedAt = m.CreatedAt
	return m.ID, nil
}

// Update 全量更新用户记录。
func (r *userRepo) Update(ctx context.Context, u *biz.User) error {
	m := fromBiz(u)
	m.ID = u.ID

	if err := r.data.db.WithContext(ctx).Save(m).Error; err != nil {
		return fmt.Errorf("data: 更新用户失败: %w", err)
	}
	return nil
}

// ── 内部工具 ────────────────────────────────────────────────────────────────

// normalize 把存储层错误归一化为领域错误。
//
// 这一层归一化是刻意的：业务层因此不需要 import gorm，
// 更换 ORM 或改用云数据库 SDK 时不会被牵连。
func normalize(err error, action string) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return biz.ErrUserNotFound
	}
	return fmt.Errorf("data: %s失败: %w", action, err)
}

// isDuplicateKey 判断是否为唯一约束冲突。
//
// 这里直接比对 PostgreSQL 的 SQLSTATE 码，而未引入官方错误码常量包：
// 该码由 SQL 标准固定，几乎不会变化，少一个依赖更利于长期维护。
func isDuplicateKey(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}

// toBiz 把存储模型转换为业务实体。
func toBiz(m *userModel) *biz.User {
	return &biz.User{
		ID:        m.ID,
		Email:     m.Email,
		Password:  m.Password,
		Nickname:  m.Nickname,
		Gender:    m.Gender,
		AvatarURL: m.AvatarURL,
		CreatedAt: m.CreatedAt,
	}
}

// fromBiz 把业务实体转换为存储模型。
func fromBiz(u *biz.User) *userModel {
	return &userModel{
		ID:        u.ID,
		Email:     u.Email,
		Password:  u.Password,
		Nickname:  u.Nickname,
		Gender:    u.Gender,
		AvatarURL: u.AvatarURL,
		CreatedAt: u.CreatedAt,
	}
}
