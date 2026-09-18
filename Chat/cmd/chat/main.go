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
		// 配置错误时日志组件尚未就绪，只能直接写标准错误后退出。
		// 这是刻意的 fail-fast：带着错误配置启动比启动失败更危险。
		fmt.Fprintf(os.Stderr, "[chat] 配置加载失败: %v\n", err)
		os.Exit(1)
	}

	logger, err := xlog.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[chat] 初始化日志失败: %v\n", err)
		os.Exit(1)
	}
	log.SetLogger(logger)

	app, cleanup, err := initApp(cfg, logger)
	if err != nil {
		log.Fatalf("初始化应用失败: %v", err)
	}
	defer cleanup()

	if err := app.Run(); err != nil {
		log.Fatalf("应用运行结束: %v", err)
	}
}
