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
	"github.com/Jesse-467/im/Chat/internal/ws"
	"github.com/Jesse-467/im/Chat/internal/xid"
)

// initApp 的装配实现由 Wire 在编译期生成到 wire_gen.go，运行时零反射开销。
func initApp(cfg *conf.Config, logger klog.Logger) (*kratos.App, func(), error) {
	panic(wire.Build(
		data.ProviderSet,
		biz.ProviderSet,
		service.ProviderSet,
		server.ProviderSet,
		ws.ProviderSet,
		mq.NewPublisher,
		newRelay,
		newWSServer,
		newApp,
	))
}

// newRelay 构造 Outbox 投递协程。
func newRelay(outbox biz.OutboxRepo, pub mq.Publisher, logger klog.Logger) *relay.Relay {
	return relay.New(outbox, pub, logger)
}

// newWSServer 构造 WebSocket 网关，并把下行推送能力回注给业务服务。
//
// 这里存在一个双向依赖：网关需要 ChatService 处理上行消息，
// 而 ChatService 需要网关推送下行消息。用显式装配函数打破：
// 先构造网关（此时它的 handler 由闭包延迟绑定），
// 再把网关作为 Pusher 注入 ChatService。
//
// 之所以不在构造函数里互相传入：那会形成无法解开的环；
// 用延迟绑定可以让依赖方向在运行时单向化，也让「推送是可选能力」
// 这一事实在代码结构上直接可见。
func newWSServer(
	cfg *conf.Config,
	registry *ws.Registry,
	presence *ws.Presence,
	gen *xid.Generator,
	chatSvc *service.ChatService,
	logger klog.Logger,
) *ws.Server {
	srv := ws.NewServer(cfg, registry, presence, gen, chatSvc.HandleUpstream, logger)
	// 把网关作为推送实现注入业务服务，完成双向连接的闭环
	chatSvc.SetPusher(registry)
	return srv
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
	wsSrv *ws.Server,
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
		kratos.Server(hs, wsSrv),
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
