// Package service 是业务用例对外的适配层。
//
// 职责边界：只做「参数绑定 → DTO 转换 → 调用 biz → 错误码映射 → 组装响应」。
// 任何业务判断都属于 biz 层，本层不得出现 if/else 形式的业务分支。
//
// 同一个 AccountService 同时实现 gRPC 接口与 Gin 处理函数，
// 因此 HTTP 与 gRPC 两条链路共享完全相同的业务语义，不会出现行为漂移。
package service

import (
	"context"
	"errors"

	klog "github.com/go-kratos/kratos/v2/log"

	accountv1 "github.com/Jesse-467/im/pkg/account/v1"

	"github.com/Jesse-467/im/Account/internal/auth"
	"github.com/Jesse-467/im/Account/internal/biz"
	"github.com/Jesse-467/im/Account/internal/conf"
	"github.com/Jesse-467/im/Account/internal/errs"
)

// AccountService 是账号中心的服务实现。
type AccountService struct {
	// 内嵌未实现版本，保证接口新增方法时本服务仍可编译
	accountv1.UnimplementedAccountServiceServer

	uc  *biz.UserUseCase
	cfg *conf.Config
	log *klog.Helper
}

// NewAccountService 构造服务实现。
func NewAccountService(uc *biz.UserUseCase, cfg *conf.Config, logger klog.Logger) *AccountService {
	return &AccountService{
		uc:  uc,
		cfg: cfg,
		log: klog.NewHelper(klog.With(logger, "module", "service/account")),
	}
}

// Register 注册账号。
func (s *AccountService) Register(ctx context.Context, req *accountv1.RegisterRequest) (*accountv1.RegisterResponse, error) {
	u, err := s.uc.Register(ctx, req.GetEmail(), req.GetPassword(), req.GetNickname(), int32(req.GetGender()))
	if err != nil {
		return nil, toErrs(err)
	}
	return &accountv1.RegisterResponse{UserId: u.ID}, nil
}

// Login 登录并签发访问令牌。
func (s *AccountService) Login(ctx context.Context, req *accountv1.LoginRequest) (*accountv1.LoginResponse, error) {
	u, err := s.uc.Login(ctx, req.GetEmail(), req.GetPassword())
	if err != nil {
		return nil, toErrs(err)
	}

	token, err := auth.Sign(s.cfg.App.JWTSecret, u.ID, s.cfg.App.JWTAccessExpire)
	if err != nil {
		return nil, toErrs(err)
	}

	return &accountv1.LoginResponse{
		UserId:       u.ID,
		AccessToken:  token.AccessToken,
		AccessExpire: token.ExpireAt,
	}, nil
}

// VerifyToken 校验访问令牌。供 Chat 的 WebSocket 网关与网关层使用。
func (s *AccountService) VerifyToken(ctx context.Context, req *accountv1.VerifyTokenRequest) (*accountv1.VerifyTokenResponse, error) {
	uid, expireAt, err := auth.Parse(s.cfg.App.JWTSecret, req.GetAccessToken())
	if err != nil {
		// 令牌无效属于正常业务分支，不作为错误返回，交由调用方按 valid 字段处理
		return &accountv1.VerifyTokenResponse{Valid: false}, nil
	}
	return &accountv1.VerifyTokenResponse{Valid: true, UserId: uid, ExpireAt: expireAt}, nil
}

// GetUser 查询单个用户资料。
//
// 仅当查询者就是本人（或系统调用 viewer_id=0）时才返回邮箱等隐私字段。
func (s *AccountService) GetUser(ctx context.Context, req *accountv1.GetUserRequest) (*accountv1.GetUserResponse, error) {
	u, err := s.uc.GetUser(ctx, req.GetUserId())
	if err != nil {
		return nil, toErrs(err)
	}

	withPrivate := req.GetViewerId() == 0 || req.GetViewerId() == u.ID
	return &accountv1.GetUserResponse{Profile: toProfile(u, withPrivate)}, nil
}

// BatchGetUsers 批量查询用户资料，供 Chat 一次性填充会话中的用户信息。
func (s *AccountService) BatchGetUsers(ctx context.Context, req *accountv1.BatchGetUsersRequest) (*accountv1.BatchGetUsersResponse, error) {
	users, err := s.uc.BatchGetUsers(ctx, req.GetUserIds())
	if err != nil {
		return nil, toErrs(err)
	}

	profiles := make(map[int64]*accountv1.UserProfile, len(users))
	for id, u := range users {
		// 批量接口不返回隐私字段
		profiles[id] = toProfile(u, false)
	}
	return &accountv1.BatchGetUsersResponse{Profiles: profiles}, nil
}

// ModifyUserInfo 修改用户资料。
func (s *AccountService) ModifyUserInfo(ctx context.Context, req *accountv1.ModifyUserInfoRequest) (*accountv1.ModifyUserInfoResponse, error) {
	_, err := s.uc.ModifyUserInfo(ctx, req.GetUserId(), req.GetNickname(), int32(req.GetGender()), req.GetAvatarUrl())
	if err != nil {
		return nil, toErrs(err)
	}
	return &accountv1.ModifyUserInfoResponse{Success: true}, nil
}

// ResetPassword 修改密码。
func (s *AccountService) ResetPassword(ctx context.Context, req *accountv1.ResetPasswordRequest) (*accountv1.ResetPasswordResponse, error) {
	err := s.uc.ResetPassword(ctx, req.GetEmail(), req.GetOldPassword(), req.GetNewPassword())
	if err != nil {
		return nil, toErrs(err)
	}
	return &accountv1.ResetPasswordResponse{Success: true}, nil
}

// ── 内部工具 ────────────────────────────────────────────────────────────────

// toErrs 把领域错误翻译为对外错误码。
//
// 这是分层的关键收口点：biz 层只表达「发生了什么」，
// 由这里决定「对外的 HTTP / gRPC 错误码是什么」。
func toErrs(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, biz.ErrUserNotFound):
		return errs.Wrap(err, errs.CodeNoData, "")
	case errors.Is(err, biz.ErrEmailTaken):
		return errs.Wrap(err, errs.CodeDataExist, "")
	case errors.Is(err, biz.ErrInvalidCredentials):
		return errs.Wrap(err, errs.CodeUnauthorized, "")
	case errors.Is(err, biz.ErrInvalidParam):
		return errs.Wrap(err, errs.CodeParamError, "")
	default:
		return errs.Wrap(err, errs.CodeServerError, "")
	}
}

// toProfile 把业务实体转换为对外资料结构。
func toProfile(u *biz.User, withPrivate bool) *accountv1.UserProfile {
	p := &accountv1.UserProfile{
		UserId:    u.ID,
		Nickname:  u.Nickname,
		Gender:    accountv1.Gender(u.Gender),
		AvatarUrl: u.AvatarURL,
		CreatedAt: u.CreatedAt.UnixMilli(),
	}
	if withPrivate {
		p.Email = u.Email
	}
	return p
}
