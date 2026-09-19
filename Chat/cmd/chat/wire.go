//go:build wireinject
// +build wireinject

package main

import (
	"context"
	"time"

	"github.com/go-kratos/kratos/v2"
	klog "github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"

	"github.com/Jesse-467/im/Chat/internal/biz"
	"github.com/Jesse-467/im/Chat/internal/conf"
	"github.com/Jesse-467/im/Chat/internal/data"
	"github.com/Jesse-467/im/Chat/internal/mq"
	"github.com/Jesse-467/im/Chat/internal/relay"
	"github.com/Jesse-467/im/Chat/internal/server"
	"github.com/Jesse-467/im/Chat/internal/service"
)

// initApp 的装配实现由 Wire 在编译期生成到 wire_gen.go，运行时零反射开销。
//
// 依赖关系完全由各层的 ProviderSet 声明（data → biz → service → server），
// 新增组件只需改动对应的 ProviderSet，无需手工维护初始化顺序。
func initApp(cfg *conf.Config, logger klog.Logger) (*kratos.App, func(), error) {
	panic(wire.Build(
		data.ProviderSet,
		biz.ProviderSet,
		service.ProviderSet,
		server.ProviderSet,
		mq.NewPublisher,
		newRelay,
		newApp,
	))
}

// newRelay 构造 Outbox 投递协程。
func newRelay(outbox biz.OutboxRepo, pub mq.Publisher, logger klog.Logger) *relay.Relay {
	return relay.New(outbox, pub, logger)
}

// newApp 组装 Kratos 应用。
//
// 投递协程通过 Kratos 的生命周期钩子接入：
//   - AfterStart 在传输层就绪后启动，避免服务还没能接受请求就开始推送；
//   - BeforeStop 在传输层关闭前取消，让当前这一轮投递有机会收尾，
//     而不是在投递途中被强杀、留下状态不明的事件。
func newApp(
	cfg *conf.Config,
	logger klog.Logger,
	hs *server.HTTPServer,
	r *relay.Relay,
) *kratos.App {
	// relayCtx 的生命周期与应用绑定：cancel 在退出时调用
	relayCtx, cancelRelay := context.WithCancel(context.Background())

	return kratos.New(
		kratos.Name(cfg.ServiceName),
		kratos.Version(Version),
		kratos.Logger(logger),
		// 优雅停止超时：为在途请求与连接排空留出时间
		kratos.StopTimeout(15*time.Second),
		kratos.Server(hs),
		kratos.AfterStart(func(_ context.Context) error {
			go r.Run(relayCtx)
			return nil
		}),
		kratos.BeforeStop(func(_ context.Context) error {
			// 先停投递，再关传输层：避免在服务已经不可用时还在产生新的推送
			cancelRelay()
			return nil
		}),
	)
}
