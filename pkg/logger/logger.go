package logger

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/INKCR0W/jm-automation/internal/config"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var log *zap.Logger

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
	log = zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))

	return nil
}

func Sync() {
	if log != nil {
		_ = log.Sync()
	}
}

func Debug(msg string, fields ...interface{}) {
	log.Sugar().Debugw(msg, fields...)
}

func Info(msg string, fields ...interface{}) {
	log.Sugar().Infow(msg, fields...)
}

func Warn(msg string, fields ...interface{}) {
	log.Sugar().Warnw(msg, fields...)
}

func Error(msg string, fields ...interface{}) {
	log.Sugar().Errorw(msg, fields...)
}
