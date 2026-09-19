package main

import (
	"fmt"
	"os"

	"github.com/go-kratos/kratos/v2/log"

	"github.com/Jesse-467/im/Chat/internal/conf"
	"github.com/Jesse-467/im/Chat/internal/xlog"
)

// Version 由构建时注入：-ldflags "-X main.Version=$(git describe)"
var Version = "dev"

// main 的职责刻意保持最小：装载配置 → 初始化日志 → 交给 Wire 装配好的应用运行。
//
// 全部依赖关系都在 wire.go 中声明式表达，main 里不出现任何手工组装代码，
// 新增组件时只需修改 ProviderSet，不会污染入口。
func main() {
	cfg, err := conf.Load()
	if err != nil {
		// 配置存在致命问题时日志组件尚未就绪，只能直接写标准错误。
		//
		// 这是刻意的 fail-fast：带着错误配置启动，故障会在第一次业务请求时
		// 才暴露，且现场信息（如究竟哪一项没配）往往已被淹没。
		// 把问题在启动瞬间列全，比运行期排查代价低得多。
		fmt.Fprintf(os.Stderr, "\n[chat] 启动中止\n%v\n\n", err)
		os.Exit(1)
	}

	logger, err := xlog.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[chat] 初始化日志失败: %v\n", err)
		os.Exit(1)
	}
	log.SetLogger(logger)

	// 可降级的配置问题在这里告警：日志组件已就绪，能走统一的日志格式与采集。
	// 刻意不阻断启动——缓存、消息队列等组件缺失时服务仍可接受请求，
	// 只是相关能力受限，应当让运维看到告警而不是让服务彻底不可用。
	cfg.ReportDegradations(func(format string, args ...any) {
		log.NewHelper(log.With(logger, "module", "conf")).Warnf(format, args...)
	})

	app, cleanup, err := initApp(cfg, logger)
	if err != nil {
		log.Fatalf("初始化应用失败: %v", err)
	}
	defer cleanup()

	if err := app.Run(); err != nil {
		log.Fatalf("应用运行结束: %v", err)
	}
}
