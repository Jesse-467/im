package server

import (
	"time"

	klog "github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware/logging"
	"github.com/go-kratos/kratos/v2/middleware/recovery"
	"github.com/go-kratos/kratos/v2/middleware/tracing"
	kgrpc "github.com/go-kratos/kratos/v2/transport/grpc"

	accountv1 "github.com/Jesse-467/im/pkg/account/v1"

	"github.com/Jesse-467/im/Account/internal/conf"
	"github.com/Jesse-467/im/Account/internal/service"
)

// NewGRPCServer 构建 gRPC 服务。
//
// 供 Chat 服务通过 BatchGetUsers / VerifyToken 等接口读取账号数据，
// 也是本服务对外唯一的服务间通信入口。
func NewGRPCServer(c *conf.Config, logger klog.Logger, svc *service.AccountService) *kgrpc.Server {
	opts := []kgrpc.ServerOption{
		kgrpc.Address(c.App.GRPCAddr),
		kgrpc.Timeout(10 * time.Second),
		kgrpc.Middleware(
			// 顺序即执行顺序：先兜住 panic，再埋点，最后记录访问日志
			recovery.Recovery(),
			tracing.Server(),
			logging.Server(logger),
		),
	}
	// 生产环境关闭反射，避免暴露内部接口结构
	if !c.Debug() {
		opts = append(opts, kgrpc.DisableReflection())
	}

	srv := kgrpc.NewServer(opts...)
	accountv1.RegisterAccountServiceServer(srv, svc)

	return srv
}
