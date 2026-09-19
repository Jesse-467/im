package ws

import (
	"github.com/google/wire"

	"github.com/Jesse-467/im/Chat/internal/data"
)

// NodeName 是网关节点标识的具名类型。
//
// 与 data.NodeID 同义但在本包内独立声明：Presence 只关心它是字符串，
// 用本包的类型可以避免依赖注入时与其他 string 参数冲突。
type NodeName string

// ProviderSet 是 WebSocket 网关的依赖注入集合。
//
// 刻意不包含 NewServer：网关与 ChatService 互相依赖，需要用
// cmd 层的显式装配函数打破这个环，因此 Server 由那里构造。
var ProviderSet = wire.NewSet(
	NewRegistry,
	NewPresence,
	ProvideNodeName,
)

// ProvideNodeName 把 data.NodeID 转为本包使用的节点名。
//
// 必须导出：Wire 生成的装配代码位于 cmd 包的 main 包中，
// 无法调用其他包的未导出函数。
func ProvideNodeName(id data.NodeID) NodeName {
	return NodeName(id)
}
