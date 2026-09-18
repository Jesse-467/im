// Package xlog 构建本服务的结构化日志。
//
// 对外返回 kratos 的 log.Logger，从而与框架的中间件、helper 完全兼容；
// 底层实现使用 zap，支持 json / text 两种编码，便于本地阅读与线上采集。
package xlog

import (
	"fmt"
	"os"

	klog "github.com/go-kratos/kratos/v2/log"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/Jesse-467/im/Account/internal/conf"
)

// New 依据配置构建日志实例。
func New(c *conf.Config) (klog.Logger, error) {
	level, err := zapcore.ParseLevel(c.Log.Level)
	if err != nil {
		return nil, fmt.Errorf("xlog: 解析 LOG_LEVEL=%q 失败: %w", c.Log.Level, err)
	}

	encCfg := zapcore.EncoderConfig{
		TimeKey:        "ts",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     klog.DefaultMessageKey,
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.MillisDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	var encoder zapcore.Encoder
	if c.Log.Format == "text" {
		encoder = zapcore.NewConsoleEncoder(encCfg)
	} else {
		encoder = zapcore.NewJSONEncoder(encCfg)
	}

	core := zapcore.NewCore(encoder, zapcore.AddSync(os.Stdout), level)
	// 跳过桥接层与 kratos helper 两层栈帧，让 caller 指向真正的业务调用点
	z := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(2))

	return &zapLogger{sugar: z.Sugar(), env: c.Environment, service: c.ServiceName}, nil
}

// zapLogger 把 zap 适配为 kratos 的 log.Logger。
type zapLogger struct {
	sugar   *zap.SugaredLogger
	env     string
	service string
}

// Log 实现 kratos log.Logger。
//
// kratos 约定 keyvals 成对出现，其中 msg 为消息体，其余作为结构化字段。
// 这里统一注入 env / service，避免每条日志重复携带。
func (l *zapLogger) Log(level klog.Level, keyvals ...any) error {
	if len(keyvals) == 0 {
		return nil
	}

	fields := make([]any, 0, len(keyvals))
	msg := ""
	for i := 0; i+1 < len(keyvals); i += 2 {
		key := fmt.Sprint(keyvals[i])
		val := keyvals[i+1]
		if key == klog.DefaultMessageKey {
			msg = fmt.Sprint(val)
			continue
		}
		fields = append(fields, key, val)
	}
	fields = append(fields, "env", l.env, "service", l.service)

	switch level {
	case klog.LevelDebug:
		l.sugar.Debugw(msg, fields...)
	case klog.LevelInfo:
		l.sugar.Infow(msg, fields...)
	case klog.LevelWarn:
		l.sugar.Warnw(msg, fields...)
	case klog.LevelError:
		l.sugar.Errorw(msg, fields...)
	default:
		l.sugar.Fatalw(msg, fields...)
	}
	return nil
}
