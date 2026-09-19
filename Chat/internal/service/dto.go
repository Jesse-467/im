package service

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Jesse-467/im/Chat/internal/auth"
	"github.com/Jesse-467/im/Chat/internal/biz"
	"github.com/Jesse-467/im/Chat/internal/errs"
	"github.com/Jesse-467/im/Chat/internal/httpx"
)

// 本文件是 HTTP 传输层的公共部分：请求/响应 DTO 与转换函数。
//
// 为什么需要独立 DTO 而不是直接复用 protobuf 结构体：
// protoc-gen-go 生成的 json tag 使用 proto 字段名（如 conversation_id），
// 而对外接口约定使用小驼峰（conversationId）。两者演进节奏也不同——
// proto 面向服务间契约，HTTP DTO 面向客户端，用独立 DTO 可以让它们互不牵制。

// ── 会话标识的兼容处理 ──────────────────────────────────────────────────────

// 早期接口用字符串 groupId 作为会话标识，且单聊用的是 "minUid_maxUid" 这一
// 非稳定形式。新契约统一用 int64 的 conversationId。
//
// 兼容策略：请求同时接受两种字段；响应同时返回两种字段（groupId 为数字字符串）。
// 旧客户端把服务端返回的 groupId 原样回传即可正常工作，因此无需强制升级。

// conversationRef 是可嵌入请求 DTO 的会话标识引用。
//
// ConversationID 类型是 int64 而非 string，但支持从 JSON 字符串解码：
// 19 位雪花 ID 超出 JavaScript 的 Number.MAX_SAFE_INTEGER，浏览器用
// JSON.parse 读服务端下发的 ID 时会被静默舍入，于是通常把 ID 当字符串传递。
// 若只接受数字，这类请求会解码失败或落到 0，表现为「接口返回成功但数据为空」
// 或「会话不存在」——而问题只在 ID 足够大时出现，极难定位。
type conversationRef struct {
	ConversationID bigID  `json:"conversationId"`
	GroupID        string `json:"groupId"`
}

// resolve 优先取 conversationId，其次解析 groupId。
//
// 由 handler 显式传入 svc：DTO 不该依赖框架上下文，显式传参让数据流向一目了然。
func (r conversationRef) resolve(svc *ChatService, ctx *gin.Context) (int64, error) {
	if id := int64(r.ConversationID); id > 0 {
		return id, nil
	}
	return svc.convUC.ResolveConversationID(ctx.Request.Context(), r.GroupID)
}

// bigID 是可以从 JSON 数字或字符串解码的 int64。
//
// 用于承载雪花 ID 这类超出前端安全整数范围的标识：两种字面量都接受，
// 且字符串形式能保留完整精度（数字形式在解析前就已在前端被舍入）。
type bigID int64

// UnmarshalJSON 兼容数字与字符串两种写法。
func (b *bigID) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if s == "" || s == "null" {
		*b = 0
		return nil
	}
	// 字符串形式：去掉两侧引号后按十进制解析
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
		if s == "" {
			*b = 0
			return nil
		}
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("会话 ID 必须是整数或整数字符串，收到 %s", data)
	}
	*b = bigID(v)
	return nil
}

// ID 是对外输出大整数标识时的统一类型。
//
// 为什么输出侧也要转成字符串，而不是继续返回数字：
// 客户端（尤其是浏览器）用 JSON.parse 解析时，19 位雪花 ID 会被静默舍入成
// 另一个数（...784 变成 ...800）。这类错误没有任何异常可捕获，
// 只会在后续请求中表现为「会话不存在」「数据为空」，极难定位。
//
// 统一序列化为字符串后，客户端拿到什么就能原样回传什么。
// 注意：输入侧用 bigID，它同时接受字符串与数字，因此老客户端
// 继续传数字也不会失败。
type ID int64

// String 便于日志与断言使用。
func (i ID) String() string { return strconv.FormatInt(int64(i), 10) }

// MarshalJSON 把大整数输出为 JSON 字符串。
func (i ID) MarshalJSON() ([]byte, error) {
	return []byte(`"` + strconv.FormatInt(int64(i), 10) + `"`), nil
}

// UnmarshalJSON 允许该类型也被反序列化（兼容数字与字符串）。
func (i *ID) UnmarshalJSON(data []byte) error {
	var b bigID
	if err := b.UnmarshalJSON(data); err != nil {
		return err
	}
	*i = ID(b)
	return nil
}

// idsOf 把一组 int64 转成对外输出的 ID 列表。
func idsOf(in []int64) []ID {
	if len(in) == 0 {
		return nil
	}
	out := make([]ID, 0, len(in))
	for _, v := range in {
		out = append(out, ID(v))
	}
	return out
}

// groupIDOf 把会话 ID 转换为对外的字符串标识。
func groupIDOf(conversationID int64) string {
	return strconv.FormatInt(conversationID, 10)
}

// ── 公共响应结构 ────────────────────────────────────────────────────────────

// chatMsgDTO 对应旧接口的 ChatMsg，字段名与含义保持不变。
//
// ID / ConversationID / SenderID 是雪花值，统一用 ID 类型输出为字符串，
// 避免前端 JSON.parse 的精度丢失。Seq 不是雪花值（会话内递增的小整数），
// 保持数字类型，便于客户端直接做算术比较。
type chatMsgDTO struct {
	ID             ID     `json:"id"`
	ConversationID ID     `json:"conversationId"`
	GroupID        string `json:"groupId"`
	Seq            int64  `json:"seq"`
	SenderID       ID     `json:"senderId"`
	Type           int64  `json:"type"`
	Content        string `json:"content"`
	Uuid           string `json:"uuid"`
	CreateTime     int64  `json:"createTime"`
}

// toChatMsgDTO 把消息实体转换为对外结构。
func toChatMsgDTO(m *biz.Message) *chatMsgDTO {
	if m == nil {
		return nil
	}
	return &chatMsgDTO{
		ID:             ID(m.ID),
		ConversationID: ID(m.ConversationID),
		GroupID:        groupIDOf(m.ConversationID),
		Seq:            m.Seq,
		SenderID:       ID(m.SenderID),
		Type:           int64(m.Type),
		Content:        m.Content,
		Uuid:           m.ClientMsgID,
		CreateTime:     m.CreatedAt.UnixMilli(),
	}
}

// ── 取当前登录用户 ──────────────────────────────────────────────────────────

// currentUID 取出当前登录用户 ID。未登录或令牌无效时输出未认证响应并返回 false。
//
// 鉴权本身已由 requireAuth 中间件完成，这里取不到只可能是路由未挂到鉴权组，
// 属于编码疏漏，因此按未认证处理并保持失败关闭（fail-closed）。
func currentUID(c *gin.Context) (int64, bool) {
	uid, ok := auth.UserIDFromContext(c.Request.Context())
	if !ok {
		httpx.Fail(c, errs.New(errs.CodeUnauthorized, ""))
		return 0, false
	}
	return uid, true
}
