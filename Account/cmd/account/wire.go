//go:build wireinject
// +build wireinject

package main

import (
	"time"

	"github.com/go-kratos/kratos/v2"
	klog "github.com/go-kratos/kratos/v2/log"
	kgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
	"github.com/google/wire"

	"github.com/Jesse-467/im/Account/internal/biz"
	"github.com/Jesse-467/im/Account/internal/conf"
	"github.com/Jesse-467/im/Account/internal/data"
	"github.com/Jesse-467/im/Account/internal/server"
	"github.com/Jesse-467/im/Account/internal/service"
)

// initApp 的装配实现由 Wire 在编译期生成到 wire_gen.go，运行时零反射开销。
//
// 依赖关系完全由各层的 ProviderSet 声明，新增组件只需改动对应的 ProviderSet，
// 无需手工维护初始化顺序。
func initApp(cfg *conf.Config, logger klog.Logger) (*kratos.App, func(), error) {
	panic(wire.Build(
		data.ProviderSet,
		biz.ProviderSet,
		service.ProviderSet,
		server.ProviderSet,
		newApp,
	))
}

// newApp 组装 Kratos 应用，统一管理 HTTP 与 gRPC 两个传输层的生命周期。
//
// 这正是 Gin 实现 transport.Server 的价值：Gin 与 gRPC 由同一个 kratos.App
// 统一启停，因此优雅退出、信号处理、启动顺序都无需自己实现。
func newApp(
	cfg *conf.Config,
	logger klog.Logger,
	hs *server.HTTPServer,
	gs *kgrpc.Server,
) *kratos.App {
	return kratos.New(
		kratos.Name(cfg.ServiceName),
		kratos.Version(Version),
		kratos.Logger(logger),
		// 优雅停止超时：为在途请求与连接排空留出时间
		kratos.StopTimeout(15*time.Second),
		kratos.Server(hs, gs),
	)
}
