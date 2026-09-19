package service

import (
	"encoding/json"

	"github.com/gin-gonic/gin"

	"github.com/Jesse-467/im/Chat/internal/errs"
	"github.com/Jesse-467/im/Chat/internal/httpx"
)

// ── 会话列表（消息页首屏） ──────────────────────────────────────────────────

// messageGroupInfoListReq 旧接口不接收任何参数，用户身份来自 JWT。
type messageGroupInfoListReq struct{}

// conversationItemDTO 对应旧接口的 MessageGroupInfo。
//
// 旧字段 groupId / aliasName / avatarUrl / lastMsg 全部保留且含义不变，
// 另外补充了新契约需要的 conversationId、type、未读数等字段。
type conversationItemDTO struct {
	ConversationID ID     `json:"conversationId"`
	GroupID        string `json:"groupId"`
	// 1 单聊 2 群聊
	Type int32 `json:"type"`
	// 群聊的群名；单聊为空
	Name string `json:"name"`
	// 展示名：单聊为对方昵称或备注名，群聊为群名
	AliasName string `json:"aliasName"`
	AvatarUrl string `json:"avatarUrl"`
	// 未读数 = 会话最大序号 - 我的已读位点
	UnreadCount int64       `json:"unreadCount"`
	LastReadSeq int64       `json:"lastReadSeq"`
	MaxSeq      int64       `json:"maxSeq"`
	LastMsg     *chatMsgDTO `json:"lastMsg"`
}

type messageGroupInfoListResp struct {
	List []conversationItemDTO `json:"list"`
}

// HTTPMessageGroupInfoList 处理 POST /api/group/message_group_info_list
func (s *ChatService) HTTPMessageGroupInfoList(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		return
	}

	items, err := s.convUC.ListConversations(c.Request.Context(), uid)
	if err != nil {
		httpx.Fail(c, toErrs(err))
		return
	}

	list := make([]conversationItemDTO, 0, len(items))
	for _, it := range items {
		list = append(list, conversationItemDTO{
			ConversationID: ID(it.Conversation.ID),
			GroupID:        groupIDOf(it.Conversation.ID),
			Type:           it.Conversation.Type,
			Name:           it.Conversation.Name,
			AliasName:      it.DisplayName,
			AvatarUrl:      it.DisplayAvatar,
			UnreadCount:    it.UnreadCount,
			LastReadSeq:    it.LastReadSeq,
			MaxSeq:         it.Conversation.MaxSeq,
			LastMsg:        toChatMsgDTO(it.LastMessage),
		})
	}
	httpx.OK(c, messageGroupInfoListResp{List: list})
}

// ── 创建群聊 ────────────────────────────────────────────────────────────────

// memberIDList 是成员 ID 列表，元素同时接受 JSON 数字与字符串。
//
// 用自定义切片类型而不是 []int64：雪花 ID 超出前端安全整数范围后，
// 客户端通常会把 ID 当字符串传递，若只接受数字会导致整个请求解码失败。
// 而「建群时少传了一个成员」这类问题很难从错误信息里看出来。
type memberIDList []ID

// UnmarshalJSON 逐个元素兼容数字与字符串形式。
func (l *memberIDList) UnmarshalJSON(data []byte) error {
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return err
	}
	out := make(memberIDList, 0, len(raws))
	for _, raw := range raws {
		var id ID
		if err := id.UnmarshalJSON(raw); err != nil {
			return err
		}
		if id > 0 {
			out = append(out, id)
		}
	}
	*l = out
	return nil
}

// int64s 转换为业务层使用的 int64 切片。
func (l memberIDList) int64s() []int64 {
	if len(l) == 0 {
		return nil
	}
	out := make([]int64, 0, len(l))
	for _, v := range l {
		out = append(out, int64(v))
	}
	return out
}

type createGroupChatReq struct {
	GroupName string `json:"groupName"`
	// 初始成员，可不传
	MemberIDs memberIDList `json:"memberIds"`
	// 兼容旧客户端的历史字段名
	ToUID memberIDList `json:"toUid"`
}

// members 返回初始成员列表。
func (r createGroupChatReq) members() []int64 {
	if len(r.MemberIDs) > 0 {
		return r.MemberIDs.int64s()
	}
	return r.ToUID.int64s()
}

type createGroupChatResp struct {
	ConversationID ID     `json:"conversationId"`
	GroupID        string `json:"groupId"`
	// 实际成功加入的成员数（不含创建者），便于调用方感知无效成员被跳过
	AddedCount int32 `json:"addedCount"`
}

// HTTPCreateGroupChat 处理 POST /api/group/create_group_chat
func (s *ChatService) HTTPCreateGroupChat(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		return
	}

	var req createGroupChatReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, errs.Wrap(err, errs.CodeParamError, "参数校验失败"))
		return
	}

	conv, added, err := s.groupUC.CreateGroup(c.Request.Context(), uid, req.GroupName, req.members())
	if err != nil {
		httpx.Fail(c, toErrs(err))
		return
	}

	httpx.OK(c, createGroupChatResp{
		ConversationID: ID(conv.ID),
		GroupID:        groupIDOf(conv.ID),
		AddedCount:     int32(added),
	})
}

// ── 添加群成员 ──────────────────────────────────────────────────────────────

type addGroupChatReq struct {
	conversationRef
	// 支持两种字段名：新的 userIds 与旧的 toUid
	UserIDs memberIDList `json:"userIds"`
	ToUID   memberIDList `json:"toUid"`
}

// members 返回要加入的成员列表。
func (r addGroupChatReq) members() []int64 {
	if len(r.UserIDs) > 0 {
		return r.UserIDs.int64s()
	}
	return r.ToUID.int64s()
}

type addGroupChatResp struct {
	// 实际新增人数，已在群内的会被跳过
	Cnt int64 `json:"cnt"`
}

// HTTPAddGroupChat 处理 POST /api/group/add_group_chat
func (s *ChatService) HTTPAddGroupChat(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		return
	}

	var req addGroupChatReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, errs.Wrap(err, errs.CodeParamError, "参数校验失败"))
		return
	}

	convID, err := req.resolve(s, c)
	if err != nil {
		httpx.Fail(c, toErrs(err))
		return
	}

	added, err := s.groupUC.AddMembers(c.Request.Context(), convID, uid, req.members())
	if err != nil {
		httpx.Fail(c, toErrs(err))
		return
	}
	httpx.OK(c, addGroupChatResp{Cnt: int64(added)})
}

// ── 群内用户列表 ────────────────────────────────────────────────────────────

type groupUserListReq struct {
	conversationRef
}

type groupUserListResp struct {
	List []ID `json:"list"`
}

// HTTPGroupUserList 处理 POST /api/group/group_user_list
func (s *ChatService) HTTPGroupUserList(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		return
	}

	var req groupUserListReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, errs.Wrap(err, errs.CodeParamError, "参数校验失败"))
		return
	}

	convID, err := req.resolve(s, c)
	if err != nil {
		httpx.Fail(c, toErrs(err))
		return
	}

	ids, err := s.groupUC.ListMemberIDs(c.Request.Context(), convID, uid)
	if err != nil {
		httpx.Fail(c, toErrs(err))
		return
	}
	httpx.OK(c, groupUserListResp{List: idsOf(ids)})
}

// ── 群成员详情 ──────────────────────────────────────────────────────────────

type memberListReq struct {
	conversationRef
	// 是否填充昵称与头像，默认 true
	WithProfile *bool `json:"withProfile"`
}

type groupMemberDTO struct {
	UserID    ID     `json:"userId"`
	NickName  string `json:"nickName"`
	AvatarUrl string `json:"avatarUrl"`
	AliasName string `json:"aliasName"`
	// 0 成员 1 管理员 2 群主
	Role        int32 `json:"role"`
	LastReadSeq int64 `json:"lastReadSeq"`
	JoinedAt    int64 `json:"joinedAt"`
}

type memberListResp struct {
	List []groupMemberDTO `json:"list"`
}

// HTTPGroupMemberList 处理 POST /api/group/member_list
func (s *ChatService) HTTPGroupMemberList(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		return
	}

	var req memberListReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, errs.Wrap(err, errs.CodeParamError, "参数校验失败"))
		return
	}

	convID, err := req.resolve(s, c)
	if err != nil {
		httpx.Fail(c, toErrs(err))
		return
	}

	withProfile := true
	if req.WithProfile != nil {
		withProfile = *req.WithProfile
	}

	details, err := s.groupUC.ListMembers(c.Request.Context(), convID, uid, withProfile)
	if err != nil {
		httpx.Fail(c, toErrs(err))
		return
	}

	list := make([]groupMemberDTO, 0, len(details))
	for _, d := range details {
		item := groupMemberDTO{
			UserID:      ID(d.Member.UserID),
			AliasName:   d.Member.AliasName,
			Role:        d.Member.Role,
			LastReadSeq: d.Member.LastReadSeq,
			JoinedAt:    d.Member.JoinedAt.UnixMilli(),
		}
		if d.Brief != nil {
			item.NickName = d.Brief.Nickname
			item.AvatarUrl = d.Brief.AvatarURL
		}
		list = append(list, item)
	}
	httpx.OK(c, memberListResp{List: list})
}

// ── 退出群聊 ────────────────────────────────────────────────────────────────

type quitGroupReq struct {
	conversationRef
}

type quitGroupResp struct {
	Success bool `json:"success"`
}

// HTTPQuitGroup 处理 POST /api/group/quit
func (s *ChatService) HTTPQuitGroup(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		return
	}

	var req quitGroupReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, errs.Wrap(err, errs.CodeParamError, "参数校验失败"))
		return
	}

	convID, err := req.resolve(s, c)
	if err != nil {
		httpx.Fail(c, toErrs(err))
		return
	}

	if err := s.groupUC.QuitGroup(c.Request.Context(), convID, uid); err != nil {
		httpx.Fail(c, toErrs(err))
		return
	}
	httpx.OK(c, quitGroupResp{Success: true})
}

// ── 上报已读位点 ────────────────────────────────────────────────────────────

type markReadReq struct {
	conversationRef
	Seq int64 `json:"seq"`
}

type markReadResp struct {
	Success bool `json:"success"`
}

// HTTPMarkRead 处理 POST /api/group/mark_read
func (s *ChatService) HTTPMarkRead(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		return
	}

	var req markReadReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, errs.Wrap(err, errs.CodeParamError, "参数校验失败"))
		return
	}

	convID, err := req.resolve(s, c)
	if err != nil {
		httpx.Fail(c, toErrs(err))
		return
	}

	if err := s.convUC.MarkRead(c.Request.Context(), convID, uid, req.Seq); err != nil {
		httpx.Fail(c, toErrs(err))
		return
	}
	httpx.OK(c, markReadResp{Success: true})
}
