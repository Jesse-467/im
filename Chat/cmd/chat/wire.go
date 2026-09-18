//go:build wireinject
// +build wireinject

package main

import (
	"time"

	"github.com/go-kratos/kratos/v2"
	klog "github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"

	"github.com/Jesse-467/im/Chat/internal/conf"
	"github.com/Jesse-467/im/Chat/internal/data"
	"github.com/Jesse-467/im/Chat/internal/server"
)

// initApp 的装配实现由 Wire 在编译期生成到 wire_gen.go，运行时零反射开销。
//
// 依赖关系完全由各层的 ProviderSet 声明，新增组件只需改动对应的 ProviderSet，
// 无需手工维护初始化顺序。
//
// 与 Account 的差异：骨架阶段没有 biz / service 层，
// 待业务实现落地后在此追加 biz.ProviderSet 与 service.ProviderSet 即可。
func initApp(cfg *conf.Config, logger klog.Logger) (*kratos.App, func(), error) {
	panic(wire.Build(
		data.ProviderSet,
		server.ProviderSet,
		newApp,
	))
}

// newApp 组装 Kratos 应用，统一管理传输层的生命周期。
//
// 当前只挂载 HTTP（Gin）一个 server；Chat 的 gRPC 服务端将在业务层落地后接入，
// 届时只需把 gRPC Server 追加到 kratos.Server(...) 中，
// 优雅退出、信号处理、启动顺序仍由 kratos.App 统一负责。
func newApp(
	cfg *conf.Config,
	logger klog.Logger,
	hs *server.HTTPServer,
) *kratos.App {
	return kratos.New(
		kratos.Name(cfg.ServiceName),
		kratos.Version(Version),
		kratos.Logger(logger),
		// 优雅停止超时：为在途请求与连接排空留出时间
		kratos.StopTimeout(15*time.Second),
		kratos.Server(hs),
	)
}
