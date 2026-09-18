// Package httpx 定义 HTTP 层的统一响应封装。
//
// 独立成包的原因：Gin 的中间件在 server 包，业务处理函数在 service 包，
// 两者都需要输出统一响应。把封装下沉到这里，可以让两个包都依赖它而不互相依赖，
// 从而避免 server ↔ service 的循环引用。
package httpx

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Jesse-467/im/Chat/internal/errs"
)

// Body 是统一的 HTTP 响应结构。
//
// 沿用既有接口的 {code,msg,data} 约定：HTTP 状态码保持 200，
// 业务结果由 code 表达，从而与历史客户端保持兼容。
type Body struct {
	Code uint32 `json:"code"`
	Msg  string `json:"msg"`
	Data any    `json:"data"`
}

// OK 输出成功响应。
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Body{Code: errs.OK, Msg: "OK", Data: data})
}

// Fail 输出失败响应。
//
// 任意 error 都会被归一化为 CodeError；未知错误统一收敛为「服务繁忙」，
// 内部细节不对外暴露，仅落日志。
func Fail(c *gin.Context, err error) {
	ce := errs.FromError(err)
	c.JSON(http.StatusOK, Body{Code: ce.Code, Msg: ce.Msg, Data: nil})
}

// FailStatus 输出非 200 的失败响应，仅用于探针等需要语义化状态码的场景。
func FailStatus(c *gin.Context, status int, err error) {
	ce := errs.FromError(err)
	c.JSON(status, Body{Code: ce.Code, Msg: ce.Msg, Data: nil})
}
