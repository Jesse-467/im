package service

import (
	"strconv"

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
type conversationRef struct {
	ConversationID int64  `json:"conversationId"`
	GroupID        string `json:"groupId"`
}

// resolve 优先取 conversationId，其次解析 groupId。
//
// 由 handler 显式传入 svc：DTO 不该依赖框架上下文，显式传参让数据流向一目了然。
func (r conversationRef) resolve(svc *ChatService, ctx *gin.Context) (int64, error) {
	if r.ConversationID > 0 {
		return r.ConversationID, nil
	}
	return svc.convUC.ResolveConversationID(ctx.Request.Context(), r.GroupID)
}

// groupIDOf 把会话 ID 转换为对外的字符串标识。
func groupIDOf(conversationID int64) string {
	return strconv.FormatInt(conversationID, 10)
}

// ── 公共响应结构 ────────────────────────────────────────────────────────────

// chatMsgDTO 对应旧接口的 ChatMsg，字段名与含义保持不变。
type chatMsgDTO struct {
	ID             int64  `json:"id"`
	ConversationID int64  `json:"conversationId"`
	GroupID        string `json:"groupId"`
	Seq            int64  `json:"seq"`
	SenderID       int64  `json:"senderId"`
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
		ID:             m.ID,
		ConversationID: m.ConversationID,
		GroupID:        groupIDOf(m.ConversationID),
		Seq:            m.Seq,
		SenderID:       m.SenderID,
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
