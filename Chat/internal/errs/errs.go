// Package errs 定义聊天服务的错误码与统一错误类型。
//
// 设计要点：
//   - 错误码与既有对外接口保持兼容：0 成功 / 400x 客户端错误 / 500x 服务端错误；
//   - CodeError 实现了 grpc 的 GRPCStatus()，因此同一个错误在 HTTP 与 gRPC
//     两个传输层上都能保持一致的数值码，无需额外的转换代码；
//   - 对外只暴露安全的错误信息，内部细节通过 cause 保留，仅落日志。
//
// Chat 与 Account 不共享代码，故错误码在这里独立定义一份；数值语义保持一致，
// 以便客户端用同一套 code 处理两个服务的响应。
package errs

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// 业务错误码
const (
	OK = 0

	CodeClientError    = 4000 // 客户端错误
	CodeUnauthorized   = 4001 // 未认证或凭证无效
	CodeParamError     = 4002 // 参数校验失败
	CodeNoData         = 4003 // 数据不存在
	CodeDataExist      = 4004 // 数据已存在
	CodeTooManyRequest = 4005 // 触发限流
	CodeForbidden      = 4006 // 无权限

	CodeServerError  = 5000 // 服务端内部错误
	CodeDBError      = 5001 // 数据库操作异常
	CodeCacheError   = 5002 // 缓存操作异常
	CodeMarshalError = 5003 // 序列化异常
	CodeMQError      = 5004 // 消息队列异常
	CodeNotImpl      = 5005 // 功能未实现
)

// codeText 是错误码的默认文案，便于在未显式提供文案时兜底。
var codeText = map[uint32]string{
	CodeClientError:    "请求不合法",
	CodeUnauthorized:   "身份认证失败",
	CodeParamError:     "参数校验失败",
	CodeNoData:         "数据不存在",
	CodeDataExist:      "数据已存在",
	CodeTooManyRequest: "操作过于频繁，请稍后重试",
	CodeForbidden:      "没有操作权限",
	CodeServerError:    "服务繁忙，请稍后重试",
	CodeDBError:        "数据操作异常",
	CodeCacheError:     "缓存操作异常",
	CodeMarshalError:   "数据解析异常",
	CodeMQError:        "消息投递异常",
	CodeNotImpl:        "功能暂未开放",
}

// Text 返回错误码的默认文案。
func Text(code uint32) string {
	if s, ok := codeText[code]; ok {
		return s
	}
	return "未知错误"
}

// CodeError 是携带业务错误码的统一错误类型。
type CodeError struct {
	Code  uint32
	Msg   string
	cause error
}

// Error 实现 error 接口。对外只暴露安全的 Msg。
func (e *CodeError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("[%d] %s: %v", e.Code, e.Msg, e.cause)
	}
	return fmt.Sprintf("[%d] %s", e.Code, e.Msg)
}

// Unwrap 支持 errors.Is / errors.As 链式判断。
func (e *CodeError) Unwrap() error { return e.cause }

// GRPCStatus 让 gRPC 传输层直接使用本服务的业务错误码。
func (e *CodeError) GRPCStatus() *status.Status {
	return status.New(codes.Code(e.Code), e.Msg)
}

// Is 支持按错误码比较。
func (e *CodeError) Is(target error) bool {
	var t *CodeError
	if errors.As(target, &t) {
		return e.Code == t.Code
	}
	return false
}

// New 创建一个业务错误，msg 为空时使用错误码默认文案。
func New(code uint32, msg string) *CodeError {
	if msg == "" {
		msg = Text(code)
	}
	return &CodeError{Code: code, Msg: msg}
}

// Newf 创建一个带格式化文案的业务错误。
func Newf(code uint32, format string, args ...any) *CodeError {
	return &CodeError{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// Wrap 包装底层错误并附上业务错误码。
func Wrap(err error, code uint32, msg string) *CodeError {
	if msg == "" {
		msg = Text(code)
	}
	return &CodeError{Code: code, Msg: msg, cause: err}
}

// Wrapf 包装底层错误并附上格式化的业务文案。
func Wrapf(err error, code uint32, format string, args ...any) *CodeError {
	return &CodeError{Code: code, Msg: fmt.Sprintf(format, args...), cause: err}
}

// FromError 把任意 error 归一化为 CodeError。
//
// 未知错误一律收敛为 CodeServerError 并使用通用文案，避免把数据库语句、
// 内部地址等敏感细节透出给客户端；原始错误保留在 cause 中供日志排查。
func FromError(err error) *CodeError {
	if err == nil {
		return nil
	}

	var ce *CodeError
	if errors.As(err, &ce) {
		return ce
	}

	if s, ok := status.FromError(err); ok && s.Code() != codes.Unknown {
		return &CodeError{Code: uint32(s.Code()), Msg: s.Message(), cause: err}
	}

	return &CodeError{Code: CodeServerError, Msg: Text(CodeServerError), cause: err}
}
