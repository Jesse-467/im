package service

import (
	"github.com/gin-gonic/gin"

	"github.com/Jesse-467/im/Chat/internal/biz"
	"github.com/Jesse-467/im/Chat/internal/errs"
	"github.com/Jesse-467/im/Chat/internal/httpx"
)

// ── 添加好友 ────────────────────────────────────────────────────────────────

// addFriendReq 刻意同时接受两种字段名。
//
// 早期接口用的是 snake_case 的 user_id，新契约统一为 userId。
// 两种都收，旧客户端在升级期不会因为字段名变化而失败。
// 取值类型是 bigID，因此 userId 传数字或字符串都能正确解析。
type addFriendReq struct {
	UserID bigID `json:"userId"`
	// 兼容旧客户端的历史字段名
	LegacyUserID bigID  `json:"user_id"`
	ApplyMsg     string `json:"applyMsg"`
}

// target 返回本次要添加的目标用户 ID。
func (r addFriendReq) target() int64 {
	if r.UserID > 0 {
		return int64(r.UserID)
	}
	return int64(r.LegacyUserID)
}

type addFriendResp struct {
	// 新契约返回申请 ID，便于客户端后续查询或撤销
	RequestID ID `json:"requestId"`
	// 双方已是好友时为 true，此时不会产生新申请
	AlreadyFriends bool `json:"alreadyFriends"`
}

// HTTPAddFriend 处理 POST /api/group/add_friend
func (s *ChatService) HTTPAddFriend(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		return
	}

	var req addFriendReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, errs.Wrap(err, errs.CodeParamError, "参数校验失败"))
		return
	}

	ftReq, already, err := s.friendUC.ApplyFriend(c.Request.Context(), uid, req.target(), req.ApplyMsg)
	if err != nil {
		httpx.Fail(c, toErrs(err))
		return
	}

	resp := addFriendResp{AlreadyFriends: already}
	if ftReq != nil {
		resp.RequestID = ID(ftReq.ID)
	}
	httpx.OK(c, resp)
}

// ── 处理好友申请 ────────────────────────────────────────────────────────────

type handleFriendReq struct {
	// 新契约：直接给申请 ID。用 bigID 兼容客户端回传的字符串形式
	RequestID bigID `json:"requestId"`
	// 旧契约：给单聊会话标识，服务端据此反查待处理申请
	GroupID string `json:"groupId"`
	IsAgree bool   `json:"isAgree"`
}

type handleFriendResp struct {
	ConversationID ID     `json:"conversationId"`
	GroupID        string `json:"groupId"`
}

// HTTPHandleFriend 处理 POST /api/group/handle_friend
func (s *ChatService) HTTPHandleFriend(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		return
	}

	var req handleFriendReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, errs.Wrap(err, errs.CodeParamError, "参数校验失败"))
		return
	}

	requestID, err := s.resolveFriendRequestID(c, uid, req)
	if err != nil {
		httpx.Fail(c, toErrs(err))
		return
	}

	convID, err := s.friendUC.HandleFriend(c.Request.Context(), uid, requestID, req.IsAgree)
	if err != nil {
		httpx.Fail(c, toErrs(err))
		return
	}

	resp := handleFriendResp{ConversationID: ID(convID)}
	if convID > 0 {
		resp.GroupID = groupIDOf(convID)
	}
	httpx.OK(c, resp)
}

// resolveFriendRequestID 解析出本次要处理的好友申请 ID。
//
// 新契约直接给 requestId；旧契约只给了单聊会话标识，需要用会话定位到双方用户，
// 再反查他们之间待处理的申请。先按"对方申请我"查，再按"我申请对方"查——
// 后者覆盖了用户重复点击申请的场景。
func (s *ChatService) resolveFriendRequestID(c *gin.Context, uid int64, req handleFriendReq) (int64, error) {
	if req.RequestID > 0 {
		return int64(req.RequestID), nil
	}

	ctx := c.Request.Context()
	convID, err := s.convUC.ResolveConversationID(ctx, req.GroupID)
	if err != nil {
		return 0, err
	}
	peer, err := s.convUC.PeerOfSingle(ctx, convID, uid)
	if err != nil {
		return 0, err
	}

	if r, err := s.friendUC.FindPendingRequestBetween(ctx, peer, uid); err == nil && r != nil {
		return r.ID, nil
	}
	if r, err := s.friendUC.FindPendingRequestBetween(ctx, uid, peer); err == nil && r != nil {
		return r.ID, nil
	}
	return 0, biz.ErrFriendRequestNotFound
}

// ── 好友列表 ────────────────────────────────────────────────────────────────

type friendItemDTO struct {
	UserID    ID     `json:"userId"`
	NickName  string `json:"nickName"`
	AvatarUrl string `json:"avatarUrl"`
	Remark    string `json:"remark"`
}

type friendListResp struct {
	List []friendItemDTO `json:"list"`
}

// HTTPFriendList 处理 POST /api/friend/list
func (s *ChatService) HTTPFriendList(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		return
	}

	details, err := s.friendUC.ListFriends(c.Request.Context(), uid)
	if err != nil {
		httpx.Fail(c, toErrs(err))
		return
	}

	list := make([]friendItemDTO, 0, len(details))
	for _, d := range details {
		item := friendItemDTO{UserID: ID(d.UserID), Remark: d.Remark}
		if d.Brief != nil {
			item.NickName = d.Brief.Nickname
			item.AvatarUrl = d.Brief.AvatarURL
		}
		list = append(list, item)
	}
	httpx.OK(c, friendListResp{List: list})
}

// ── 好友申请列表 ────────────────────────────────────────────────────────────

type friendRequestListReq struct {
	// 不传表示查询全部状态；0 待处理 1 已同意 2 已拒绝 3 已过期
	Status *int32 `json:"status"`
	Limit  int    `json:"limit"`
}

type friendRequestDTO struct {
	ID        ID     `json:"id"`
	FromUID   ID     `json:"fromUid"`
	ToUID     ID     `json:"toUid"`
	ApplyMsg  string `json:"applyMsg"`
	Status    int32  `json:"status"`
	CreatedAt int64  `json:"createdAt"`
	HandledAt int64  `json:"handledAt"`
}

type friendRequestListResp struct {
	List []friendRequestDTO `json:"list"`
}

// HTTPFriendRequestList 处理 POST /api/friend/request_list
func (s *ChatService) HTTPFriendRequestList(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		return
	}

	var req friendRequestListReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, errs.Wrap(err, errs.CodeParamError, "参数校验失败"))
		return
	}

	// -1 表示不过滤状态：0 是"待处理"这一合法值，不能拿它当"未传"
	status := int32(-1)
	if req.Status != nil {
		status = *req.Status
	}

	requests, err := s.friendUC.ListFriendRequests(c.Request.Context(), uid, status, req.Limit)
	if err != nil {
		httpx.Fail(c, toErrs(err))
		return
	}

	list := make([]friendRequestDTO, 0, len(requests))
	for _, r := range requests {
		item := friendRequestDTO{
			ID:        ID(r.ID),
			FromUID:   ID(r.FromUID),
			ToUID:     ID(r.ToUID),
			ApplyMsg:  r.ApplyMsg,
			Status:    r.Status,
			CreatedAt: r.CreatedAt.UnixMilli(),
		}
		if !r.HandledAt.IsZero() {
			item.HandledAt = r.HandledAt.UnixMilli()
		}
		list = append(list, item)
	}
	httpx.OK(c, friendRequestListResp{List: list})
}
