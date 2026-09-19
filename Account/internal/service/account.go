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
	"time"

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

	uc      *biz.UserUseCase
	session *biz.SessionUseCase
	cfg     *conf.Config
	log     *klog.Helper
}

// NewAccountService 构造服务实现。
func NewAccountService(
	uc *biz.UserUseCase,
	session *biz.SessionUseCase,
	cfg *conf.Config,
	logger klog.Logger,
) *AccountService {
	return &AccountService{
		uc:      uc,
		session: session,
		cfg:     cfg,
		log:     klog.NewHelper(klog.With(logger, "module", "service/account")),
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
//
// 签发之后立即登记设备并把令牌落库，从而让「谁在线」成为可查询、可吊销的事实。
// 超出设备上限时登记过程会踢出最早的设备，被踢的列表通过 EvictedDevices 返回。
func (s *AccountService) Login(ctx context.Context, req *accountv1.LoginRequest) (*accountv1.LoginResponse, error) {
	u, err := s.uc.Login(ctx, req.GetEmail(), req.GetPassword())
	if err != nil {
		return nil, toErrs(err)
	}

	token, err := auth.Sign(s.cfg.App.JWTSecret, u.ID, s.cfg.App.JWTAccessExpire)
	if err != nil {
		s.log.Errorw("msg", "签发令牌失败", "uid", u.ID, "err", err)
		return nil, toErrs(err)
	}

	platform := req.GetPlatform()
	if platform == "" {
		platform = "unknown"
	}

	evicted, err := s.session.RegisterDevice(ctx, &biz.AuthToken{
		UserID:    u.ID,
		JTI:       token.JTI,
		DeviceID:  req.GetDeviceId(),
		Platform:  platform,
		ExpireAt:  time.Unix(token.ExpireAt, 0),
		CreatedAt: time.Now(),
	})
	if err != nil {
		// 设备登记失败不阻断登录：令牌本身已经可用。
		// 代价是多设备上限在本次登录上不生效，因此记 Error 并继续。
		s.log.Errorw("msg", "登记登录设备失败，多设备上限本次不生效",
			"uid", u.ID, "deviceId", req.GetDeviceId(), "err", err)
		evicted = nil
	}

	if len(evicted) > 0 {
		// Warn 而非 Info：踢下线是用户可感知的行为（其他端会掉线），
		// 需要能在日志里定位「是谁踢了谁、什么时候」。
		s.log.Warnw("msg", "登录导致其他设备被踢下线",
			"uid", u.ID, "deviceId", req.GetDeviceId(),
			"evicted", evicted, "maxDevices", s.session.MaxDevices())
	}

	return &accountv1.LoginResponse{
		UserId:         u.ID,
		AccessToken:    token.AccessToken,
		AccessExpire:   token.ExpireAt,
		EvictedDevices: evicted,
	}, nil
}

// VerifyToken 校验访问令牌。供 Chat 的 WebSocket 网关与网关层使用。
//
// 校验行为由 TOKEN_MODE 决定：
//   - self：只验证 JWT 自身（签名 + 过期时间），零存储依赖；
//   - db：在自校验之外还要求数据库中该令牌未被吊销，支持即时踢下线。
func (s *AccountService) VerifyToken(ctx context.Context, req *accountv1.VerifyTokenRequest) (*accountv1.VerifyTokenResponse, error) {
	tokenString := req.GetAccessToken()

	res, err := s.session.VerifyToken(ctx, func() (int64, string, int64, error) {
		return auth.ParseWithJTI(s.cfg.App.JWTSecret, tokenString)
	})
	if err != nil {
		// 存储故障或令牌与存储不一致时，如实返回错误而不是返回 valid=false：
		// 前者需要调用方重试/告警，后者会被当成「令牌过期」而静默失效。
		return nil, toErrs(err)
	}
	if !res.Valid {
		// 令牌无效属于正常业务分支，不作为错误返回，交由调用方按 valid 字段处理
		return &accountv1.VerifyTokenResponse{Valid: false}, nil
	}

	return &accountv1.VerifyTokenResponse{
		Valid:    true,
		UserId:   res.UserID,
		ExpireAt: res.ExpireAt,
	}, nil
}

// Logout 主动登出：吊销当前令牌并释放其占用的设备在线位。
func (s *AccountService) Logout(ctx context.Context, req *accountv1.LogoutRequest) (*accountv1.LogoutResponse, error) {
	tokenString := req.GetAccessToken()
	if tokenString == "" {
		return nil, toErrs(biz.ErrInvalidParam)
	}

	uid, jti, _, err := auth.ParseWithJTI(s.cfg.App.JWTSecret, tokenString)
	if err != nil {
		// 登出携带的令牌已经无效：按「已经登出」处理，返回成功而不是报错。
		// 否则客户端在令牌过期后调登出会拿到错误，被迫忽略它，反而更糟。
		s.log.Infow("msg", "登出时令牌已无效，按已登出处理")
		return &accountv1.LogoutResponse{Success: true}, nil
	}

	if err := s.session.Logout(ctx, uid, jti, req.GetDeviceId()); err != nil {
		return nil, toErrs(err)
	}
	return &accountv1.LogoutResponse{Success: true}, nil
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
//
// 改密成功后吊销该用户的全部令牌：密码变更意味着「凭据已更换」，
// 旧令牌若继续有效，任何已泄露的令牌都能绕过这次安全操作。
func (s *AccountService) ResetPassword(ctx context.Context, req *accountv1.ResetPasswordRequest) (*accountv1.ResetPasswordResponse, error) {
	err := s.uc.ResetPassword(ctx, req.GetEmail(), req.GetOldPassword(), req.GetNewPassword())
	if err != nil {
		return nil, toErrs(err)
	}

	// 需要先查出邮箱对应的用户 ID 才能吊销令牌。
	// 改密本身已经验证过旧密码，因此这次查询不引入额外的权限风险。
	if u, ferr := s.uc.GetUserByEmail(ctx, req.GetEmail()); ferr == nil && u != nil {
		if rerr := s.session.RevokeAll(ctx, u.ID, biz.RevokeReasonPasswordReset); rerr != nil {
			// 吊销失败不回滚密码变更：密码已经改了，报错会让用户以为没改成功。
			// 代价是旧令牌在自然过期前仍可用，因此记 Error 以便告警与人工介入。
			s.log.Errorw("msg", "改密后吊销全部令牌失败，旧令牌在过期前仍有效",
				"uid", u.ID, "err", rerr)
		}
	} else if ferr != nil {
		s.log.Errorw("msg", "改密后查询用户失败，无法吊销旧令牌",
			"email", req.GetEmail(), "err", ferr)
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
		// 未知错误（数据库、Redis 故障）：收敛为 5000 并保留原始错误在 cause 中，
		// 由日志记录细节，对外只暴露通用文案。
		//
		// 注意令牌失效（过期 / 被吊销）不会走到这里：VerifyToken 对这类
		// 情况返回 valid=false 而非 error，因此对外的是一句「令牌无效」，
		// 而不是服务端 5000。
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
