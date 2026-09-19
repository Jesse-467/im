package biz

import (
	"github.com/google/wire"

	"github.com/Jesse-467/im/Chat/internal/conf"
)

// ProviderSet 是 biz 层的依赖注入集合。
//
// 这里的用例之间存在同层协作：FriendUseCase 依赖 ConversationUseCase，
// 因为"好友申请通过后建立单聊会话"是好友领域自己的业务规则。
// Wire 会按依赖顺序完成装配，无需人工维护初始化顺序。
var ProviderSet = wire.NewSet(
	NewConversationUseCase,
	NewFriendUseCase,
	NewGroupUseCase,
	NewMessageUseCase,
	ProvideMessageTopic,
)

// ProvideMessageTopic 提取消息主题名供 Wire 注入。
//
// 之所以单独写一个 provider：NewMessageUseCase 需要 string 类型的 topic，
// 而 Wire 无法从 *conf.Config 自动推导出 string（那样会让所有 string 参数
// 都混在一起）。用一个具名 provider 表达意图，也让依赖关系在装配图上更明确。
func ProvideMessageTopic(c *conf.Config) string {
	return c.MQ.MessageTopic()
}
