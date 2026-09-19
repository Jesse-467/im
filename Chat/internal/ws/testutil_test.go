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
