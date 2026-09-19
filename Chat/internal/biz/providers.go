package biz

import (
	"github.com/google/wire"

	"github.com/Jesse-467/im/Chat/internal/conf"
)

// MessageTopic 是消息投递主题名。
//
// 定义为具名类型而非直接用 string：服务中需要注入的字符串不止一个
// （主题名、节点 ID 等），若都用 string，依赖注入会因类型相同而无法区分。
// 具名类型让每个字符串在类型系统里各有身份。
type MessageTopic string

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

// ProvideMessageTopic 提供消息主题名。
func ProvideMessageTopic(c *conf.Config) MessageTopic {
	return MessageTopic(c.MQ.MessageTopic())
}
