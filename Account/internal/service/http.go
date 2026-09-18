package service

import (
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
	AccessToken  string `json:"accessToken"`
	AccessExpire int64  `json:"accessExpire"`
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
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, loginResp{
		AccessToken:  resp.GetAccessToken(),
		AccessExpire: resp.GetAccessExpire(),
	})
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
