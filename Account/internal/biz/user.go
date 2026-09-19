// Package biz 实现账号领域的业务用例。
//
// 本层是整个服务的核心，刻意不依赖 Gin、gRPC、GORM、Redis 等任何框架或基础设施，
// 只依赖自己声明的 UserRepo 接口。因此它可以脱离数据库与网络做纯单元测试，
// 也保证了业务规则不会被存储实现牵着走。
package biz

import (
	"context"
	"errors"
	"math/rand/v2"
	"strings"
	"time"

	klog "github.com/go-kratos/kratos/v2/log"
	"golang.org/x/crypto/bcrypt"
)

// 领域错误。由 service 层负责翻译为对外错误码。
var (
	ErrUserNotFound       = errors.New("用户不存在")
	ErrEmailTaken         = errors.New("邮箱已被注册")
	ErrInvalidCredentials = errors.New("邮箱或密码不正确")
	ErrInvalidParam       = errors.New("参数不合法")
)

// 性别取值，与 account.v1.Gender 对齐。
const (
	GenderUnspecified int32 = 0
	GenderMale        int32 = 1
	GenderFemale      int32 = 2
)

// DefaultAvatarURL 是用户未设置头像时的默认头像。
const DefaultAvatarURL = "https://s1.ax1x.com/2022/05/25/XFaDqP.png"

// 密码长度约束
const (
	passwordMinLen = 6
	passwordMaxLen = 32
)

// User 是账号领域的用户实体。
type User struct {
	ID        int64
	Email     string
	Password  string // bcrypt 哈希，任何情况下都不得保存明文
	Nickname  string
	Gender    int32
	AvatarURL string
	CreatedAt time.Time
}

// UserRepo 是用户仓储接口，由 data 层实现。
//
// 约定：仓储实现必须把「记录不存在」归一化为 ErrUserNotFound，
// 业务层不感知底层驱动（MySQL / 云数据库）的错误类型。
type UserRepo interface {
	FindByID(ctx context.Context, id int64) (*User, error)
	FindByEmail(ctx context.Context, email string) (*User, error)
	FindByIDs(ctx context.Context, ids []int64) ([]*User, error)
	Create(ctx context.Context, u *User) (int64, error)
	Update(ctx context.Context, u *User) error
}

// UserUseCase 承载账号领域的全部用例。
type UserUseCase struct {
	repo UserRepo
	log  *klog.Helper
}

// NewUserUseCase 构造用例。Wire 会自动注入 repo 与 logger。
func NewUserUseCase(repo UserRepo, logger klog.Logger) *UserUseCase {
	return &UserUseCase{
		repo: repo,
		log:  klog.NewHelper(klog.With(logger, "module", "biz/user")),
	}
}

// Register 注册新账号。
func (uc *UserUseCase) Register(ctx context.Context, email, password, nickname string, gender int32) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !looksLikeEmail(email) {
		return nil, ErrInvalidParam
	}
	if len(password) < passwordMinLen || len(password) > passwordMaxLen {
		return nil, ErrInvalidParam
	}
	if gender != GenderMale && gender != GenderFemale && gender != GenderUnspecified {
		return nil, ErrInvalidParam
	}

	if _, err := uc.repo.FindByEmail(ctx, email); err == nil {
		return nil, ErrEmailTaken
	} else if !errors.Is(err, ErrUserNotFound) {
		return nil, err
	}

	hashed, err := hashPassword(password)
	if err != nil {
		return nil, err
	}

	nickname = strings.TrimSpace(nickname)
	if nickname == "" {
		nickname = randomNickname()
	}

	u := &User{
		Email:     email,
		Password:  hashed,
		Nickname:  nickname,
		Gender:    gender,
		AvatarURL: DefaultAvatarURL,
	}

	id, err := uc.repo.Create(ctx, u)
	if err != nil {
		return nil, err
	}
	u.ID = id
	u.Password = "" // 出参不携带凭证

	uc.log.Infow("msg", "账号注册成功", "uid", id, "email", email)
	return u, nil
}

// Login 校验凭证并返回用户实体。
func (uc *UserUseCase) Login(ctx context.Context, email, password string) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	u, err := uc.repo.FindByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// 不区分「用户不存在」与「密码错误」，避免账号枚举
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}

	if !verifyPassword(password, u.Password) {
		return nil, ErrInvalidCredentials
	}

	uc.log.Infow("msg", "账号登录成功", "uid", u.ID)
	return u, nil
}

// GetUser 查询单个用户。
func (uc *UserUseCase) GetUser(ctx context.Context, id int64) (*User, error) {
	if id <= 0 {
		return nil, ErrInvalidParam
	}
	return uc.repo.FindByID(ctx, id)
}

// GetUserByEmail 按邮箱查询用户。
//
// 供改密后吊销令牌使用：改密请求只带邮箱，而令牌是按用户 ID 组织的，
// 因此需要一个按邮箱取用户的入口。
func (uc *UserUseCase) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !looksLikeEmail(email) {
		return nil, ErrInvalidParam
	}
	return uc.repo.FindByEmail(ctx, email)
}

// BatchGetUsers 批量查询用户，返回 id -> User 映射。
//
// 供 Chat 服务一次拉取会话所需的全部用户资料，避免 N+1 次跨服务调用。
func (uc *UserUseCase) BatchGetUsers(ctx context.Context, ids []int64) (map[int64]*User, error) {
	result := make(map[int64]*User, len(ids))
	if len(ids) == 0 {
		return result, nil
	}

	users, err := uc.repo.FindByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		result[u.ID] = u
	}
	return result, nil
}

// ModifyUserInfo 修改用户资料。
//
// 空字符串或零值表示该字段不修改，因此调用方无需先查询再回填。
func (uc *UserUseCase) ModifyUserInfo(ctx context.Context, id int64, nickname string, gender int32, avatarURL string) (*User, error) {
	u, err := uc.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if nickname = strings.TrimSpace(nickname); nickname != "" {
		if len([]rune(nickname)) > 32 {
			return nil, ErrInvalidParam
		}
		u.Nickname = nickname
	}
	if gender == GenderMale || gender == GenderFemale {
		u.Gender = gender
	}
	if avatarURL = strings.TrimSpace(avatarURL); avatarURL != "" {
		u.AvatarURL = avatarURL
	}

	if err := uc.repo.Update(ctx, u); err != nil {
		return nil, err
	}
	u.Password = ""
	return u, nil
}

// ResetPassword 校验旧密码后更新为新密码。
func (uc *UserUseCase) ResetPassword(ctx context.Context, email, oldPassword, newPassword string) error {
	if len(newPassword) < passwordMinLen || len(newPassword) > passwordMaxLen {
		return ErrInvalidParam
	}

	u, err := uc.repo.FindByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return ErrInvalidCredentials
		}
		return err
	}
	if !verifyPassword(oldPassword, u.Password) {
		return ErrInvalidCredentials
	}

	hashed, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	u.Password = hashed

	if err := uc.repo.Update(ctx, u); err != nil {
		return err
	}

	uc.log.Infow("msg", "账号密码已重置", "uid", u.ID)
	return nil
}

// ── 内部工具 ────────────────────────────────────────────────────────────────

// hashPassword 使用 bcrypt 生成密码哈希。
func hashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// verifyPassword 校验明文密码与哈希是否匹配。
func verifyPassword(plain, hashed string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hashed), []byte(plain)) == nil
}

// randomNickname 生成默认昵称，避免注册时昵称为空。
func randomNickname() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	buf := make([]byte, 8)
	for i := range buf {
		buf[i] = alphabet[rand.IntN(len(alphabet))]
	}
	return "im_" + string(buf)
}

// looksLikeEmail 做轻量邮箱格式校验。
//
// 采用「够用即可」策略：真正的可达性由后续的邮箱验证流程保证，
// 这里只拦掉明显非法的输入，避免用复杂正则制造误杀。
func looksLikeEmail(email string) bool {
	at := strings.IndexByte(email, '@')
	if at <= 0 || at == len(email)-1 {
		return false
	}
	domain := email[at+1:]
	if strings.ContainsAny(email, " \t\r\n") || !strings.Contains(domain, ".") {
		return false
	}
	return strings.IndexByte(domain, '.') > 0 && !strings.HasSuffix(domain, ".")
}
