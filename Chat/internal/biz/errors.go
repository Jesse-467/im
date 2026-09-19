// Package biz 实现即时聊天领域的业务用例。
//
// 本层是整个服务的核心，刻意不依赖 Gin、gRPC、GORM、Redis 等任何框架或基础设施，
// 只依赖自己声明的仓储接口与外部能力接口（如 UserProvider）。
// 因此它可以脱离数据库与网络做纯单元测试，也保证业务规则不会被存储实现牵着走。
package biz

import "errors"

// 领域错误。由 service 层统一翻译为对外错误码，biz 层不关心 HTTP/gRPC。
var (
	// 通用
	ErrInvalidParam   = errors.New("参数不合法")
	ErrNoPermission   = errors.New("没有操作权限")
	ErrNotImplemented = errors.New("功能暂未实现")

	// 会话
	ErrConversationNotFound    = errors.New("会话不存在")
	ErrNotConversationMember   = errors.New("不是会话成员")
	ErrConversationTypeInvalid = errors.New("会话类型不合法")

	// 好友
	ErrFriendRequestNotFound = errors.New("好友申请不存在")
	ErrFriendRequestHandled  = errors.New("好友申请已被处理")
	ErrAlreadyFriends        = errors.New("已经是好友")
	ErrCannotAddSelf         = errors.New("不能添加自己为好友")
	ErrFriendRequestPending  = errors.New("已有待处理的好友申请")
	ErrNotFriend             = errors.New("对方不是你的好友")

	// 群组
	ErrGroupNotFound        = errors.New("群聊不存在")
	ErrNotGroupOwner        = errors.New("只有群主可以执行该操作")
	ErrGroupOwnerCannotQuit = errors.New("群主不能直接退群")
	ErrCannotRemoveOwner    = errors.New("不能移除群主")
	ErrAlreadyGroupMember   = errors.New("对方已在群聊中")
	// ErrGroupMemberLimitExceeded 与 ErrInvalidParam 分开，便于客户端区分
	// 「参数写错了」与「群满了」——前者要改请求，后者要换策略（如新建群）。
	ErrGroupMemberLimitExceeded = errors.New("群成员数已达上限")

	// 消息
	ErrMessageNotFound      = errors.New("消息不存在")
	ErrMessageNotRecallable = errors.New("该消息无法撤回（不存在、非本人发送或已撤回）")
	ErrSeqAllocFailed       = errors.New("消息序号分配失败，请重试")

	// 账号与登录
	ErrAccountUnavailable = errors.New("账号中心暂时不可用，请稍后重试")
)
