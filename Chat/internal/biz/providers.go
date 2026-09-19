package biz

import "github.com/google/wire"

// ProviderSet 是 biz 层的依赖注入集合。
//
// 这里的用例之间存在同层协作：FriendUseCase 依赖 ConversationUseCase，
// 因为"好友申请通过后建立单聊会话"是好友领域自己的业务规则。
// Wire 会按依赖顺序完成装配，无需人工维护初始化顺序。
var ProviderSet = wire.NewSet(
	NewConversationUseCase,
	NewFriendUseCase,
	NewGroupUseCase,
)
