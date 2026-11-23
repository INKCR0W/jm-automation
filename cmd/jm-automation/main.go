package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/INKCR0W/jm-automation/internal/config"
	"github.com/INKCR0W/jm-automation/internal/scheduler"
	"github.com/INKCR0W/jm-automation/pkg/logger"
)

var (
	version   = "dev"
	buildTime = "unknown"
)

func main() {
	os.Exit(run())
}

func run() int {
	var (
		configPath  = flag.String("config", "config.yaml", "配置文件路径")
		showVersion = flag.Bool("version", false, "显示版本信息")
		runOnce     = flag.Bool("once", false, "立即执行一次任务")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("jm-automation %s (built at %s)\n", version, buildTime)
		return 0
	}

	// 加载配置
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		return 1
	}

	// 初始化日志
	if logErr := logger.Init(cfg.Log); logErr != nil {
		fmt.Fprintf(os.Stderr, "初始化日志失败: %v\n", logErr)
		return 1
	}
	defer logger.Sync()

	logger.Info("程序启动", "version", version, "config", *configPath)

	ctx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	// 创建调度器
	sched, err := scheduler.New(ctx, cfg)
	if err != nil {
		logger.Error("创建调度器失败", "error", err)
		return 1
	}

	if *runOnce {
		// 立即执行一次
		logger.Info("执行一次性任务")
		if err := sched.RunOnce(ctx); err != nil {
			logger.Error("任务执行失败", "error", err)
			return 1
		}
		logger.Info("任务执行完成")
		return 0
	}

	// 启动定时任务
	if err := sched.Start(ctx); err != nil {
		logger.Error("启动调度器失败", "error", err)
		return 1
	}

	logger.Info("调度器已启动，等待执行...")

	// 等待退出信号
	<-ctx.Done()
	stopSignals()
	logger.Info("收到退出信号，正在关闭...")
	sched.Stop()
	logger.Info("程序已退出")
	return 0
}
