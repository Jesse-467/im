package errs

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestNewUsesDefaultText 验证未提供文案时回落到错误码的默认文案。
//
// 默认文案保证客户端总能拿到可展示的提示，而不是空字符串。
func TestNewUsesDefaultText(t *testing.T) {
	e := New(CodeParamError, "")
	if e.Msg != Text(CodeParamError) {
		t.Fatalf("Msg = %q, 期望 %q", e.Msg, Text(CodeParamError))
	}
	if e.Code != CodeParamError {
		t.Fatalf("Code = %d", e.Code)
	}
}

// TestNewKeepsExplicitText 验证显式文案优先于默认文案。
func TestNewKeepsExplicitText(t *testing.T) {
	e := New(CodeParamError, "昵称不能为空")
	if e.Msg != "昵称不能为空" {
		t.Fatalf("Msg = %q", e.Msg)
	}
}

// TestTextUnknownCode 验证未知错误码有兜底文案。
func TestTextUnknownCode(t *testing.T) {
	if got := Text(99999); got != "未知错误" {
		t.Fatalf("未知错误码文案 = %q", got)
	}
}

// TestWrapPreservesCause 验证包装后仍能取回底层错误。
//
// biz 层与 data 层会反复包装同一个错误，若 Unwrap 链断裂，
// errors.Is 判断就会失效，表现为「明明是重复数据却报了 500」。
func TestWrapPreservesCause(t *testing.T) {
	sentinel := errors.New("底层数据库错误")
	e := Wrap(sentinel, CodeDBError, "入库失败")

	if !errors.Is(e, sentinel) {
		t.Error("errors.Is 未能穿透到 cause")
	}
	if e.Code != CodeDBError {
		t.Fatalf("Code = %d", e.Code)
	}
	if e.Msg != "入库失败" {
		t.Fatalf("Msg = %q", e.Msg)
	}
	// 对外错误串包含 cause，便于本地排查
	if got := e.Error(); got == "" {
		t.Error("Error() 不应为空")
	}
}

// TestErrorOmitsCauseWhenAbsent 验证无 cause 时输出不包含多余的冒号。
func TestErrorOmitsCauseWhenAbsent(t *testing.T) {
	e := New(CodeNoData, "会话不存在")
	want := "[4003] 会话不存在"
	if e.Error() != want {
		t.Fatalf("Error() = %q, 期望 %q", e.Error(), want)
	}
}

// TestIsMatchesByCode 验证按错误码比较，而非按实例或文案。
//
// 这是关键语义：调用方（如 service 层的错误映射）只关心错误类别，
// 不该因为文案不同就漏判。
func TestIsMatchesByCode(t *testing.T) {
	a := New(CodeForbidden, "不是群成员")
	b := New(CodeForbidden, "不是好友")
	c := New(CodeNoData, "不是群成员")

	if !errors.Is(a, b) {
		t.Error("相同错误码应判定相等")
	}
	if errors.Is(a, c) {
		t.Error("不同错误码不应判定相等")
	}
}

// TestFromErrorPassthroughCodeError 验证 CodeError 原样透传。
func TestFromErrorPassthroughCodeError(t *testing.T) {
	orig := New(CodeForbidden, "没有权限")
	got := FromError(orig)
	if got != orig {
		t.Fatal("已带错误码的错误应原样返回，而不是重新包装")
	}
}

// TestFromErrorHidesInternalDetails 验证未知错误不泄露内部细节。
//
// 数据库语句、内部地址等若透出给客户端既是安全隐患，
// 也会让攻击者据此推断系统结构，因此必须收敛为通用文案。
func TestFromErrorHidesInternalDetails(t *testing.T) {
	secret := errors.New(`pq: relation "conversation_member" does not exist at 10.0.0.5:5432`)
	got := FromError(secret)

	if got.Code != CodeServerError {
		t.Fatalf("Code = %d, 期望 %d", got.Code, CodeServerError)
	}
	if got.Msg != Text(CodeServerError) {
		t.Fatalf("Msg = %q, 不应包含内部细节", got.Msg)
	}
	// 内部细节必须仍保留在 cause 中，否则排查时无从下手
	if !errors.Is(got, secret) {
		t.Error("原始错误应保留在 cause 中")
	}
}

// TestFromErrorNil 验证 nil 输入返回 nil，不构造无意义的错误。
func TestFromErrorNil(t *testing.T) {
	if got := FromError(nil); got != nil {
		t.Fatalf("nil 输入应返回 nil, 得到 %+v", got)
	}
}

// TestFromErrorRecognizesGRPCStatus 验证 gRPC 状态错误被识别。
//
// Chat 作为 RPC 客户端调用账号中心时，拿到的就是这类错误；
// 若直接当成未知错误，调用方就无法区分「对方说参数错」与「对方挂了」。
func TestFromErrorRecognizesGRPCStatus(t *testing.T) {
	grpcErr := status.Error(codes.NotFound, "用户不存在")
	got := FromError(grpcErr)

	if got.Code != uint32(codes.NotFound) {
		t.Fatalf("Code = %d, 期望 %d", got.Code, codes.NotFound)
	}
	if got.Msg != "用户不存在" {
		t.Fatalf("Msg = %q", got.Msg)
	}
}

// TestGRPCStatusCarriesBusinessCode 验证同一错误在 gRPC 上保持数值码一致。
//
// 这让 HTTP 与 gRPC 两个入口对外语义统一，客户端可用同一套 code 处理。
func TestGRPCStatusCarriesBusinessCode(t *testing.T) {
	e := New(CodeParamError, "参数校验失败")
	st := e.GRPCStatus()

	if st.Code() != codes.Code(CodeParamError) {
		t.Fatalf("gRPC code = %d, 期望 %d", st.Code(), CodeParamError)
	}
	if st.Message() != "参数校验失败" {
		t.Fatalf("gRPC message = %q", st.Message())
	}
}

// TestWrapfFormatsMessage 验证格式化文案生效且保留 cause。
func TestWrapfFormatsMessage(t *testing.T) {
	cause := errors.New("timeout")
	e := Wrapf(cause, CodeMQError, "投递 %s 失败", "im.message")

	if e.Msg != "投递 im.message 失败" {
		t.Fatalf("Msg = %q", e.Msg)
	}
	if !errors.Is(e, cause) {
		t.Error("cause 丢失")
	}
}

// TestNewfFormatsMessage 验证格式化文案生效。
func TestNewfFormatsMessage(t *testing.T) {
	e := Newf(CodeParamError, "字段 %s 长度需在 %d~%d 之间", "nickName", 1, 20)
	want := fmt.Sprintf("字段 nickName 长度需在 %d~%d 之间", 1, 20)
	if e.Msg != want {
		t.Fatalf("Msg = %q, 期望 %q", e.Msg, want)
	}
}
