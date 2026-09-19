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
	"github.com/Jesse-467/im/Chat/internal/consumer"
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
		consumer.ProviderSet,
		mq.NewPublisher,
		mq.NewSubscriber,
		newRelay,
		newWSServer,
		newConsumer,
		newApp,
	))
}

// newConsumer 构造消息消费者。
//
// 显式组装的原因：消费者需要一个「只暴露推送与本地在线判断」的窄接口，
// 而 ws.Registry 的方法集更大。在这里做一次收窄，
// 可以让 consumer 包不必认识连接注册中心的完整能力。
func newConsumer(
	convRepo biz.ConversationRepo,
	loader consumer.EventLoader,
	registry *ws.Registry,
	logger klog.Logger,
) *consumer.Consumer {
	return consumer.New(convRepo, loader, registry, logger)
}

// newRelay 构造 Outbox 投递协程。
func newRelay(outbox biz.OutboxRepo, pub mq.Publisher, logger klog.Logger) *relay.Relay {
	return relay.New(outbox, pub, logger)
}

// newWSServer 构造 WebSocket 网关。
//
// 网关需要 ChatService 作为上行处理器；下行推送则全部由消息消费者完成，
// 因此依赖方向是单向的（ws → service），无需打破循环。
func newWSServer(
	cfg *conf.Config,
	registry *ws.Registry,
	presence *ws.Presence,
	gen *xid.Generator,
	chatSvc *service.ChatService,
	logger klog.Logger,
) *ws.Server {
	return ws.NewServer(cfg, registry, presence, gen, chatSvc.HandleUpstream, logger)
}

// newApp 组装 Kratos 应用。
//
// 后台协程通过 Kratos 的生命周期钩子接入：
//   - AfterStart 在传输层就绪后启动，避免服务还没能接受请求就开始推送；
//   - BeforeStop 在传输层关闭前取消，让当前这一轮处理有机会收尾，
//     而不是在处理途中被强杀、留下状态不明的事件。
func newApp(
	cfg *conf.Config,
	logger klog.Logger,
	hs *server.HTTPServer,
	wsSrv *ws.Server,
	r *relay.Relay,
	c *consumer.Consumer,
	sub mq.Subscriber,
	pub mq.Publisher,
	registry *ws.Registry,
) *kratos.App {
	// 后台协程的生命周期与应用绑定：cancel 在退出时调用
	bgCtx, cancelBg := context.WithCancel(context.Background())

	// 日志模式没有真实队列，把消费者直接注册到发布者上，
	// 使「发送 → 落库 → 投递 → 消费 → 推送」在无中间件时也能完整跑通。
	if lp, ok := pub.(*mq.LogPublisher); ok {
		lp.RegisterHandler(c.Handle)
	}

	return kratos.New(
		kratos.Name(cfg.ServiceName),
		kratos.Version(Version),
		kratos.Logger(logger),
		// 优雅停止超时：为在途请求与连接排空留出时间
		kratos.StopTimeout(15*time.Second),
		kratos.Server(hs, wsSrv),
		kratos.AfterStart(func(_ context.Context) error {
			// 生产侧：把 Outbox 事件搬到消息队列
			go r.Run(bgCtx)

			// 消费侧：Kafka 模式下由订阅协程从队列消费。
			// 消费者组按节点隔离：每个实例都要收到全量消息，
			// 才能把消息推给自己节点上的在线用户。
			if _, isLog := pub.(*mq.LogPublisher); !isLog {
				go func() {
					topic := cfg.MQ.MessageTopic()
					if err := sub.Subscribe(bgCtx, topic, registry.NodeID(), c.Handle); err != nil {
						klog.NewHelper(logger).Errorw("msg", "消息订阅退出", "err", err)
					}
				}()
			}
			return nil
		}),
		kratos.BeforeStop(func(_ context.Context) error {
			// 先停后台协程，再关传输层：避免在服务已经不可用时还在产生新的推送
			cancelBg()
			return nil
		}),
	)
}
