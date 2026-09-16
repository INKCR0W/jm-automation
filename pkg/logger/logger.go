package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/INKCR0W/jm-automation/internal/config"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	logMu sync.RWMutex
	log   *zap.Logger
)

// Init 之前打日志不该把进程搞崩，这时退化成往 stderr 输出
func current() *zap.Logger {
	logMu.RLock()
	l := log
	logMu.RUnlock()

	if l != nil {
		return l
	}
	return fallbackLogger()
}

var (
	fallbackOnce sync.Once
	fallback     *zap.Logger
)

func fallbackLogger() *zap.Logger {
	fallbackOnce.Do(func() {
		core := zapcore.NewCore(
			zapcore.NewConsoleEncoder(zap.NewProductionEncoderConfig()),
			zapcore.AddSync(os.Stderr),
			zapcore.InfoLevel,
		)
		fallback = zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))
	})
	return fallback
}

func Init(cfg config.LogConfig) error {
	// 确保日志目录存在
	if cfg.File != "" {
		dir := filepath.Dir(cfg.File)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("创建日志目录失败: %w", err)
		}
	}

	// 日志级别
	level := zapcore.InfoLevel
	switch cfg.Level {
	case "debug":
		level = zapcore.DebugLevel
	case "warn":
		level = zapcore.WarnLevel
	case "error":
		level = zapcore.ErrorLevel
	}

	// 编码配置
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.CapitalLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	// 文件输出
	var cores []zapcore.Core
	if cfg.File != "" {
		fileWriter := &lumberjack.Logger{
			Filename:   cfg.File,
			MaxSize:    cfg.MaxSize,
			MaxBackups: cfg.MaxBackups,
			Compress:   true,
		}
		cores = append(cores, zapcore.NewCore(
			zapcore.NewJSONEncoder(encoderConfig),
			zapcore.AddSync(fileWriter),
			level,
		))
	}

	// 控制台输出
	cores = append(cores, zapcore.NewCore(
		zapcore.NewConsoleEncoder(encoderConfig),
		zapcore.AddSync(os.Stdout),
		level,
	))

	core := zapcore.NewTee(cores...)

	logMu.Lock()
	log = zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))
	logMu.Unlock()

	return nil
}

func Sync() {
	logMu.RLock()
	l := log
	logMu.RUnlock()

	if l != nil {
		// 忽略 Sync 错误，因为在某些平台上可能会失败
		_ = l.Sync()
	}
}

func Debug(msg string, fields ...interface{}) {
	current().Sugar().Debugw(msg, fields...)
}

func Info(msg string, fields ...interface{}) {
	current().Sugar().Infow(msg, fields...)
}

func Warn(msg string, fields ...interface{}) {
	current().Sugar().Warnw(msg, fields...)
}

func Error(msg string, fields ...interface{}) {
	current().Sugar().Errorw(msg, fields...)
}
