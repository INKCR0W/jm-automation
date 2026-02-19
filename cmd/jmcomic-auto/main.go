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
	var (
		configPath  = flag.String("config", "config.yaml", "配置文件路径")
		showVersion = flag.Bool("version", false, "显示版本信息")
		runOnce     = flag.Bool("once", false, "立即执行一次任务")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("jmcomic-auto %s (built at %s)\n", version, buildTime)
		return
	}

	// 加载配置
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}

	// 初始化日志
	if err := logger.Init(cfg.Log); err != nil {
		fmt.Fprintf(os.Stderr, "初始化日志失败: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	logger.Info("程序启动", "version", version, "config", *configPath)

	// 创建调度器
	sched, err := scheduler.New(cfg)
	if err != nil {
		logger.Error("创建调度器失败", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 处理系统信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	if *runOnce {
		// 立即执行一次
		logger.Info("执行一次性任务")
		if err := sched.RunOnce(ctx); err != nil {
			logger.Error("任务执行失败", "error", err)
			os.Exit(1)
		}
		logger.Info("任务执行完成")
		return
	}

	// 启动定时任务
	if err := sched.Start(ctx); err != nil {
		logger.Error("启动调度器失败", "error", err)
		os.Exit(1)
	}

	logger.Info("调度器已启动，等待执行...")

	// 等待退出信号
	<-sigCh
	logger.Info("收到退出信号，正在关闭...")
	cancel()
	sched.Stop()
	logger.Info("程序已退出")
}
