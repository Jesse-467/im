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
)
