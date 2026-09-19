package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	klog "github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/transport"

	"github.com/Jesse-467/im/Account/internal/auth"
	"github.com/Jesse-467/im/Account/internal/conf"
	"github.com/Jesse-467/im/Account/internal/errs"
	"github.com/Jesse-467/im/Account/internal/health"
	"github.com/Jesse-467/im/Account/internal/httpx"
	"github.com/Jesse-467/im/Account/internal/service"
)

// 编译期断言：Gin 服务必须满足 Kratos 的传输层契约。
//
// 这样它就能与 gRPC Server 一起交给 kratos.New 统一管理生命周期与优雅启停：
// 既保留 Gin 的路由与中间件生态，又能复用 Kratos 的服务治理能力。
var (
	_ transport.Server     = (*HTTPServer)(nil)
	_ transport.Endpointer = (*HTTPServer)(nil)
)

// HTTPServer 是基于 Gin 的 HTTP 传输层实现。
type HTTPServer struct {
	*gin.Engine // 暴露引擎，便于后续按需追加路由或中间件

	srv  *http.Server
	addr string
	log  *klog.Helper
}

// NewHTTPServer 构建 HTTP 服务并注册全部路由。
func NewHTTPServer(
	c *conf.Config,
	logger klog.Logger,
	accountSvc *service.AccountService,
	checkers []health.Checker,
) *HTTPServer {
	if c.IsProd() {
		gin.SetMode(gin.ReleaseMode)
	}

	engine := gin.New()
	engine.Use(
		gin.Recovery(),
		requestLogger(logger),
	)

	s := &HTTPServer{
		Engine: engine,
		addr:   c.App.HTTPAddr,
		log:    klog.NewHelper(klog.With(logger, "module", "server/http")),
		srv: &http.Server{
			Addr:              c.App.HTTPAddr,
			Handler:           engine,
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       90 * time.Second,
		},
	}

	s.registerProbes(c, checkers)
	s.registerRoutes(c, accountSvc)

	return s
}

// registerProbes 注册存活与就绪探针。
//
// /healthz 只表明进程存活，不触碰外部依赖；
// /readyz  真实探测数据库与缓存，未就绪时返回 503，供负载均衡摘流。
func (s *HTTPServer) registerProbes(c *conf.Config, checkers []health.Checker) {
	s.GET("/healthz", func(ctx *gin.Context) {
		httpx.OK(ctx, gin.H{
			"status":  "ok",
			"service": c.ServiceName,
			"env":     c.Environment,
		})
	})

	s.GET("/readyz", func(ctx *gin.Context) {
		if len(checkers) == 0 {
			httpx.OK(ctx, gin.H{"ready": true, "dependencies": []health.Result{}})
			return
		}

		probeCtx, cancel := health.WithTimeout(ctx.Request.Context(), 3*time.Second)
		defer cancel()

		results := health.CheckAll(probeCtx, checkers)
		if !health.Healthy(results) {
			ctx.JSON(http.StatusServiceUnavailable, httpx.Body{
				Code: errs.CodeServerError,
				Msg:  "依赖不可用",
				Data: gin.H{"ready": false, "dependencies": results},
			})
			return
		}
		httpx.OK(ctx, gin.H{"ready": true, "dependencies": results})
	})
}

// registerRoutes 注册业务路由。
//
// 路径沿用既有接口约定，保证对外功能表现一致；请求与响应字段使用独立的
// HTTP DTO，与 protobuf 定义解耦，便于两侧各自演进。
func (s *HTTPServer) registerRoutes(c *conf.Config, svc *service.AccountService) {
	public := s.Group("/api/user")
	{
		public.POST("/register", svc.HTTPRegister)
		public.POST("/login", svc.HTTPLogin)
	}

	secured := s.Group("/api/user", requireAuth(c.App.JWTSecret))
	{
		secured.POST("/personal_info", svc.HTTPPersonalInfo)
		secured.POST("/query_user_info", svc.HTTPQueryUserInfo)
		secured.POST("/reset_password", svc.HTTPResetPassword)
		secured.POST("/modify_personal_info", svc.HTTPModifyPersonalInfo)
		secured.POST("/logout", svc.HTTPLogout)
	}
}

// requireAuth 校验 Authorization 头中的 Bearer 令牌，并把 uid 注入 context。
func requireAuth(secret string) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		raw := ctx.GetHeader("Authorization")
		token := strings.TrimSpace(strings.TrimPrefix(raw, "Bearer "))
		if token == "" {
			httpx.Fail(ctx, errs.New(errs.CodeUnauthorized, ""))
			ctx.Abort()
			return
		}

		uid, _, err := auth.Parse(secret, token)
		if err != nil {
			httpx.Fail(ctx, errs.Wrap(err, errs.CodeUnauthorized, ""))
			ctx.Abort()
			return
		}

		ctx.Request = ctx.Request.WithContext(auth.WithUserID(ctx.Request.Context(), uid))
		ctx.Next()
	}
}

// requestLogger 记录访问日志，包含耗时、状态码与客户端 IP。
func requestLogger(logger klog.Logger) gin.HandlerFunc {
	l := klog.NewHelper(klog.With(logger, "module", "middleware/access"))

	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		if raw := c.Request.URL.RawQuery; raw != "" {
			path = path + "?" + raw
		}

		c.Next()

		l.Infow(
			"msg", "access",
			"method", c.Request.Method,
			"path", path,
			"status", c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(),
			"client_ip", c.ClientIP(),
		)
	}
}

// Start 启动 HTTP 服务，阻塞直到服务退出。
func (s *HTTPServer) Start(_ context.Context) error {
	s.log.Infof("HTTP 服务启动，监听 %s", s.addr)

	if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Stop 优雅关闭：停止接收新请求，等待在途请求处理完毕。
func (s *HTTPServer) Stop(ctx context.Context) error {
	s.log.Infof("HTTP 服务关闭，地址 %s", s.addr)
	return s.srv.Shutdown(ctx)
}

// Endpoint 返回服务地址，供服务注册使用。
func (s *HTTPServer) Endpoint() (*url.URL, error) {
	return url.Parse("http://" + s.addr)
}
