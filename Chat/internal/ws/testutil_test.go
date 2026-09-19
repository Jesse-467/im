package ws

import (
	klog "github.com/go-kratos/kratos/v2/log"
)

// newTestLogger 构造丢弃输出的日志器，避免测试输出被日志淹没。
//
// 用 klog.NewStdLogger(io.Discard) 而非 nil：部分 kratos 辅助函数
// 会直接调用 logger 的方法，传 nil 会 panic。
func newTestLogger() klog.Logger {
	return klog.NewStdLogger(discard{})
}

// discard 是丢弃全部写入的 io.Writer。
type discard struct{}

// Write 丢弃数据并报告全部写入成功。
func (discard) Write(p []byte) (int, error) { return len(p), nil }

// testNodeID 是测试用的节点标识。
const testNodeID = "node-test"

// newTestRegistry 构造不依赖 Redis 的注册中心。
//
// Registry 只需要从 Presence 读取节点 ID，其余能力（跨节点路由）在
// 单节点测试中不会被触发。因此这里直接构造一个只带 nodeID 的
// Presence 即可，无需启动 Redis——否则连接管理的单元测试会依赖外部服务。
func newTestRegistry() *Registry {
	return NewRegistry(&Presence{nodeID: testNodeID, log: klog.NewHelper(newTestLogger())}, newTestLogger())
}
