package task

import (
	"context"
	"fmt"
	"time"

	"github.com/INKCR0W/jm-automation/internal/api"
	"github.com/INKCR0W/jm-automation/internal/client"
	"github.com/INKCR0W/jm-automation/internal/config"
	"github.com/INKCR0W/jm-automation/pkg/logger"
	"github.com/INKCR0W/jm-automation/pkg/utils"
)

type Executor struct {
	client *client.Client
}

func NewExecutor(c *client.Client) *Executor {
	return &Executor{client: c}
}

func (e *Executor) Execute(ctx context.Context, account config.Account) error {
	logger.Info("开始执行任务", "username", account.Username)

	// 1. 登录
	authAPI := api.NewAuthAPI(e.client)
	loginData, err := authAPI.Login(ctx, account.Username, account.Password)
	if err != nil {
		return fmt.Errorf("登录失败: %w", err)
	}

	logger.Info("登录成功", "username", account.Username, "uid", loginData.UID, "level", loginData.Level)

	// 随机延迟，模拟人工操作
	utils.RandomDelay(1*time.Second, 3*time.Second)

	// 执行签到
	checkInAPI := api.NewCheckInAPI(e.client, authAPI.GetUserID())
	if err := checkInAPI.PerformCheckIn(ctx); err != nil {
		logger.Error("签到失败", "error", err)
		return fmt.Errorf("签到失败: %w", err)
	}

	logger.Info("任务执行完成", "username", account.Username)
	return nil
}
