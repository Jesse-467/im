package service

import (
	"github.com/gin-gonic/gin"

	"github.com/Jesse-467/im/Chat/internal/biz"
	"github.com/Jesse-467/im/Chat/internal/errs"
	"github.com/Jesse-467/im/Chat/internal/httpx"
)

// ── 发送消息（对齐旧接口 POST /api/message/upload）──────────────────────────

// uploadReq 兼容新旧两套字段：
//
//   - 旧契约用字符串 groupId，且 uuid 为客户端幂等键；
//   - 新契约用 conversationId，clientMsgId 为幂等键。
//
// 两者都接受，旧客户端无需改造。
type uploadReq struct {
	conversationRef
	Type    int64  `json:"type"`
	Content string `json:"content"`
	// 旧字段名
	Uuid string `json:"uuid"`
	// 新字段名
	ClientMsgID string `json:"clientMsgId"`
	Extra       string `json:"extra"`
}

// clientMsgID 返回本次请求的幂等键。
func (r uploadReq) clientMsgID() string {
	if r.ClientMsgID != "" {
		return r.ClientMsgID
	}
	return r.Uuid
}

type uploadResp struct {
	ID             ID     `json:"id"`
	ConversationID ID     `json:"conversationId"`
	GroupID        string `json:"groupId"`
	Seq            int64  `json:"seq"`
	CreateTime     int64  `json:"createTime"`
	// Duplicated 表示命中幂等键、返回的是已存在的消息。
	// 旧契约没有该字段，纯新增，便于客户端识别重试结果。
	Duplicated bool `json:"duplicated"`
}

// HTTPUpload 处理 POST /api/message/upload
func (s *ChatService) HTTPUpload(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		return
	}

	var req uploadReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, errs.Wrap(err, errs.CodeParamError, "参数校验失败"))
		return
	}

	convID, err := req.resolve(s, c)
	if err != nil {
		httpx.Fail(c, toErrs(err))
		return
	}

	res, err := s.msgUC.Send(c.Request.Context(), &biz.SendMessageRequest{
		ConversationID: convID,
		SenderID:       uid,
		Type:           int32(req.Type),
		Content:        req.Content,
		Extra:          req.Extra,
		ClientMsgID:    req.clientMsgID(),
	})
	if err != nil {
		httpx.Fail(c, toErrs(err))
		return
	}

	msg := res.Message
	httpx.OK(c, uploadResp{
		ID:             ID(msg.ID),
		ConversationID: ID(msg.ConversationID),
		GroupID:        groupIDOf(msg.ConversationID),
		Seq:            msg.Seq,
		CreateTime:     msg.CreatedAt.UnixMilli(),
		Duplicated:     res.Duplicated,
	})
}

// ── 拉取消息（对齐旧接口 POST /api/message/pull）────────────────────────────

type pullReq struct {
	conversationRef
	// 旧契约用 maxMsgId 表示"拉取小于该 id 的消息"，语义等价于新契约的 toSeq
	Platform string `json:"platform"`
	MaxMsgID int64  `json:"maxMsgId"`
	// 新契约字段
	FromSeq   int64 `json:"fromSeq"`
	ToSeq     int64 `json:"toSeq"`
	Limit     int   `json:"limit"`
	Ascending *bool `json:"ascending"`
}

type pullResp struct {
	List []*chatMsgDTO `json:"list"`
	// 以下为新增字段，旧客户端可忽略
	HasMore bool  `json:"hasMore"`
	MaxSeq  int64 `json:"maxSeq"`
}

// HTTPPull 处理 POST /api/message/pull
//
// 默认按降序返回（历史翻页），与旧契约一致；客户端传 ascending=true
// 即切换为升序，用于离线补齐。
func (s *ChatService) HTTPPull(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		return
	}

	var req pullReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, errs.Wrap(err, errs.CodeParamError, "参数校验失败"))
		return
	}

	convID, err := req.resolve(s, c)
	if err != nil {
		httpx.Fail(c, toErrs(err))
		return
	}

	// 旧字段 maxMsgId 走的是消息 ID，而新契约按 seq 过滤。
	// 这里不做 ID → seq 的转换：消息 ID 是雪花值，与 seq 无对应关系。
	// 旧客户端若继续传 maxMsgId，等价于"不限上界"，即拉最新的一页。
	toSeq := req.ToSeq
	if toSeq == 0 {
		toSeq = req.MaxMsgID
	}

	ascending := false
	if req.Ascending != nil {
		ascending = *req.Ascending
	}

	res, err := s.msgUC.Pull(c.Request.Context(), &biz.PullMessagesRequest{
		ConversationID: convID,
		UserID:         uid,
		FromSeq:        req.FromSeq,
		ToSeq:          toSeq,
		Limit:          req.Limit,
		Ascending:      ascending,
	})
	if err != nil {
		httpx.Fail(c, toErrs(err))
		return
	}

	list := make([]*chatMsgDTO, 0, len(res.Messages))
	for _, m := range res.Messages {
		list = append(list, toChatMsgDTO(m))
	}

	httpx.OK(c, pullResp{
		List:    list,
		HasMore: res.HasMore,
		MaxSeq:  res.MaxSeq,
	})
}

// ── 离线同步（新接口 POST /api/message/sync）────────────────────────────────

type syncReq struct {
	conversationRef
	// FromSeq 是客户端已读位点，服务端返回它之后的消息
	FromSeq int64 `json:"fromSeq"`
	Limit   int   `json:"limit"`
}

type syncResp struct {
	List []*chatMsgDTO `json:"list"`
	// HasMore 为 true 时客户端应继续以返回的 MaxSeq 作为新的 fromSeq 再次拉取
	HasMore bool  `json:"hasMore"`
	MaxSeq  int64 `json:"maxSeq"`
}

// HTTPSync 处理 POST /api/message/sync
//
// 语义上等价于 pull(ascending=true)，单独开一个路径是为了让客户端的
// 「重连补齐」与「历史翻页」两种意图在调用侧就能区分清楚，
// 也便于后续对补洞路径单独做限流与监控。
func (s *ChatService) HTTPSync(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		return
	}

	var req syncReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, errs.Wrap(err, errs.CodeParamError, "参数校验失败"))
		return
	}

	convID, err := req.resolve(s, c)
	if err != nil {
		httpx.Fail(c, toErrs(err))
		return
	}

	res, err := s.msgUC.Pull(c.Request.Context(), &biz.PullMessagesRequest{
		ConversationID: convID,
		UserID:         uid,
		FromSeq:        req.FromSeq,
		Limit:          req.Limit,
		Ascending:      true,
	})
	if err != nil {
		httpx.Fail(c, toErrs(err))
		return
	}

	list := make([]*chatMsgDTO, 0, len(res.Messages))
	for _, m := range res.Messages {
		list = append(list, toChatMsgDTO(m))
	}

	httpx.OK(c, syncResp{
		List:    list,
		HasMore: res.HasMore,
		MaxSeq:  res.MaxSeq,
	})
}

// ── 撤回消息（新接口 POST /api/message/recall）──────────────────────────────

type recallReq struct {
	conversationRef
	// MessageID 是雪花值，用 bigID 接收以兼容客户端回传的字符串形式
	MessageID bigID `json:"messageId"`
}

type recallResp struct {
	Success bool `json:"success"`
}

// HTTPRecall 处理 POST /api/message/recall
func (s *ChatService) HTTPRecall(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		return
	}

	var req recallReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, errs.Wrap(err, errs.CodeParamError, "参数校验失败"))
		return
	}

	convID, err := req.resolve(s, c)
	if err != nil {
		httpx.Fail(c, toErrs(err))
		return
	}

	if err := s.msgUC.Recall(c.Request.Context(), convID, int64(req.MessageID), uid); err != nil {
		httpx.Fail(c, toErrs(err))
		return
	}
	httpx.OK(c, recallResp{Success: true})
}
