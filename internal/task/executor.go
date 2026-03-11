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

	const maxLoginRetries = 3
	var loginData *api.LoginData
	var err error

	authAPI := api.NewAuthAPI(e.client)
	for retryCount := 1; retryCount <= maxLoginRetries; retryCount++ {
		loginData, err = authAPI.Login(ctx, account.Username, account.Password)
		if err == nil {
			break
		}

		logger.Error("登录失败", "retry", retryCount, "max_retries", maxLoginRetries, "error", err)
		if retryCount < maxLoginRetries {
			backoff := time.Duration(1<<(retryCount-1)) * time.Second
			logger.Info("等待重试", "backoff", backoff)
			time.Sleep(backoff)
			continue
		}
		return fmt.Errorf("登录失败（已重试%d次）: %w", maxLoginRetries, err)
	}

	logger.Info("登录成功", "username", account.Username, "uid", loginData.UID, "level", loginData.Level)

	utils.RandomDelay(1*time.Second, 3*time.Second)

	checkInAPI := api.NewCheckInAPI(e.client, authAPI.GetUserID())
	if err := checkInAPI.PerformCheckIn(ctx); err != nil {
		logger.Error("签到失败", "error", err)
		return fmt.Errorf("签到失败: %w", err)
	}

	logger.Info("任务执行完成", "username", account.Username)
	return nil
}
