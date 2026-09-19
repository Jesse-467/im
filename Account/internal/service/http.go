package service

import (
	"strings"

	"github.com/gin-gonic/gin"

	accountv1 "github.com/Jesse-467/im/pkg/account/v1"

	"github.com/Jesse-467/im/Account/internal/auth"
	"github.com/Jesse-467/im/Account/internal/errs"
	"github.com/Jesse-467/im/Account/internal/httpx"
)

// 本文件是 HTTP 传输层的适配代码：定义请求/响应 DTO 并绑定路由处理函数。
//
// 为什么需要独立的 DTO 而不是直接复用 protobuf 结构体：
// protoc-gen-go 生成的 json tag 使用 proto 字段名（如 user_id），而对外接口约定
// 使用小驼峰（userId）。两者的演进节奏也不同——proto 面向服务间契约，
// HTTP DTO 面向客户端。用独立 DTO 可以让两者互不牵制。

// ── 请求 DTO ────────────────────────────────────────────────────────────────

type registerReq struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6,max=32"`
	NickName string `json:"nickName"`
	Gender   int32  `json:"gender"`
}

type loginReq struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6,max=32"`
	// DeviceId 标识「同一台设备」，用于多设备在线上限的计数与踢出。
	// 客户端应在本地持久化它（如首次启动生成的 UUID），否则每次登录
	// 都会被当成一台新设备。
	DeviceId string `json:"deviceId"`
	// Platform 仅用于展示与排障，如 web / ios / android
	Platform string `json:"platform"`
}

type logoutReq struct {
	// AccessToken 待吊销的令牌。也支持放在 Authorization 头（见处理函数）。
	AccessToken string `json:"accessToken"`
	DeviceId    string `json:"deviceId"`
}

type queryUserInfoReq struct {
	UserId int64 `json:"userId" binding:"required"`
}

type resetPasswordReq struct {
	Email       string `json:"email" binding:"required,email"`
	OldPassword string `json:"oldPassword" binding:"required,min=6,max=32"`
	NewPassword string `json:"newPassword" binding:"required,min=6,max=32"`
}

type modifyPersonalInfoReq struct {
	NickName  string `json:"nickName"`
	Gender    int32  `json:"gender"`
	AvatarUrl string `json:"avatarUrl"`
}

// ── 响应 DTO ────────────────────────────────────────────────────────────────

// registerResp 与既有接口保持一致：注册成功不返回额外数据。
type registerResp struct{}

type loginResp struct {
	// 新增字段：客户端拿到令牌后通常还需要自己的用户 ID 来标识会话，
	// 返回它可省掉一次资料查询。纯新增，不影响既有客户端。
	UserId       int64  `json:"userId"`
	AccessToken  string `json:"accessToken"`
	AccessExpire int64  `json:"accessExpire"`
	// EvictedDevices 是本次登录因超出设备数上限而被踢下线的设备标识。
	//
	// 用 omitempty：绝大多数登录没有设备被踢，省略空数组可让响应体保持简洁，
	// 同时客户端判断「是否有设备被踢」只需检查字段是否存在。
	EvictedDevices []string `json:"evictedDevices,omitempty"`
}

type logoutResp struct {
	Success bool `json:"success"`
}

type profileResp struct {
	UserId    int64  `json:"userId"`
	NickName  string `json:"nickName"`
	Gender    int32  `json:"gender"`
	Email     string `json:"email"`
	AvatarUrl string `json:"avatarUrl"`
}

type resetPasswordResp struct {
	Success bool `json:"success"`
}

type modifyPersonalInfoResp struct {
	Success bool `json:"success"`
}

// ── 处理函数 ────────────────────────────────────────────────────────────────

// HTTPRegister 处理 POST /api/user/register
func (s *AccountService) HTTPRegister(c *gin.Context) {
	var req registerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, errs.Wrap(err, errs.CodeParamError, "参数校验失败"))
		return
	}

	_, err := s.Register(c.Request.Context(), &accountv1.RegisterRequest{
		Email:    req.Email,
		Password: req.Password,
		Nickname: req.NickName,
		Gender:   accountv1.Gender(req.Gender),
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, registerResp{})
}

// HTTPLogin 处理 POST /api/user/login
func (s *AccountService) HTTPLogin(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, errs.Wrap(err, errs.CodeParamError, "参数校验失败"))
		return
	}

	resp, err := s.Login(c.Request.Context(), &accountv1.LoginRequest{
		Email:    req.Email,
		Password: req.Password,
		DeviceId: req.DeviceId,
		Platform: req.Platform,
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, loginResp{
		UserId:         resp.GetUserId(),
		AccessToken:    resp.GetAccessToken(),
		AccessExpire:   resp.GetAccessExpire(),
		EvictedDevices: resp.GetEvictedDevices(),
	})
}

// HTTPLogout 处理 POST /api/user/logout，吊销当前令牌并释放设备在线位。
//
// 令牌来源优先级：请求体 accessToken > Authorization 头。
// 两者都接受是因为调用场景不同：已登录客户端习惯用头，
// 而「退出登录」页面可能只想显式传令牌。
func (s *AccountService) HTTPLogout(c *gin.Context) {
	var req logoutReq
	// 允许空请求体（仅靠 Authorization 头）：绑定失败且体为空时不报错
	if err := c.ShouldBindJSON(&req); err != nil && err.Error() != "EOF" {
		httpx.Fail(c, errs.Wrap(err, errs.CodeParamError, "参数校验失败"))
		return
	}

	token := req.AccessToken
	if token == "" {
		token = bearerToken(c)
	}
	if token == "" {
		httpx.Fail(c, errs.New(errs.CodeUnauthorized, ""))
		return
	}

	resp, err := s.Logout(c.Request.Context(), &accountv1.LogoutRequest{
		AccessToken: token,
		DeviceId:    req.DeviceId,
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, logoutResp{Success: resp.GetSuccess()})
}

// bearerToken 从 Authorization 头提取 Bearer 令牌，不存在时返回空串。
func bearerToken(c *gin.Context) string {
	raw := c.GetHeader("Authorization")
	return strings.TrimSpace(strings.TrimPrefix(raw, "Bearer "))
}

// HTTPPersonalInfo 处理 POST /api/user/personal_info，查询本人资料。
func (s *AccountService) HTTPPersonalInfo(c *gin.Context) {
	uid, ok := auth.UserIDFromContext(c.Request.Context())
	if !ok {
		httpx.Fail(c, errs.New(errs.CodeUnauthorized, ""))
		return
	}

	resp, err := s.GetUser(c.Request.Context(), &accountv1.GetUserRequest{
		ViewerId: uid,
		UserId:   uid,
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, toProfileResp(resp.GetProfile()))
}

// HTTPQueryUserInfo 处理 POST /api/user/query_user_info，按 userId 查询他人资料。
func (s *AccountService) HTTPQueryUserInfo(c *gin.Context) {
	var req queryUserInfoReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, errs.Wrap(err, errs.CodeParamError, "参数校验失败"))
		return
	}

	viewer, _ := auth.UserIDFromContext(c.Request.Context())
	resp, err := s.GetUser(c.Request.Context(), &accountv1.GetUserRequest{
		ViewerId: viewer,
		UserId:   req.UserId,
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, toProfileResp(resp.GetProfile()))
}

// HTTPResetPassword 处理 POST /api/user/reset_password
func (s *AccountService) HTTPResetPassword(c *gin.Context) {
	var req resetPasswordReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, errs.Wrap(err, errs.CodeParamError, "参数校验失败"))
		return
	}

	resp, err := s.ResetPassword(c.Request.Context(), &accountv1.ResetPasswordRequest{
		Email:       req.Email,
		OldPassword: req.OldPassword,
		NewPassword: req.NewPassword,
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, resetPasswordResp{Success: resp.GetSuccess()})
}

// HTTPModifyPersonalInfo 处理 POST /api/user/modify_personal_info
func (s *AccountService) HTTPModifyPersonalInfo(c *gin.Context) {
	var req modifyPersonalInfoReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, errs.Wrap(err, errs.CodeParamError, "参数校验失败"))
		return
	}

	uid, ok := auth.UserIDFromContext(c.Request.Context())
	if !ok {
		httpx.Fail(c, errs.New(errs.CodeUnauthorized, ""))
		return
	}

	resp, err := s.ModifyUserInfo(c.Request.Context(), &accountv1.ModifyUserInfoRequest{
		UserId:    uid,
		Nickname:  req.NickName,
		Gender:    accountv1.Gender(req.Gender),
		AvatarUrl: req.AvatarUrl,
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, modifyPersonalInfoResp{Success: resp.GetSuccess()})
}

// toProfileResp 把 protobuf 资料结构转换为 HTTP 响应 DTO。
func toProfileResp(p *accountv1.UserProfile) profileResp {
	if p == nil {
		return profileResp{}
	}
	return profileResp{
		UserId:    p.GetUserId(),
		NickName:  p.GetNickname(),
		Gender:    int32(p.GetGender()),
		Email:     p.GetEmail(),
		AvatarUrl: p.GetAvatarUrl(),
	}
}
