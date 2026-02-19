package api

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/INKCR0W/jm-automation/internal/client"
	"github.com/INKCR0W/jm-automation/pkg/logger"
)

type CheckInAPI struct {
	client *client.Client
	userID string
}

func NewCheckInAPI(c *client.Client, userID string) *CheckInAPI {
	return &CheckInAPI{
		client: c,
		userID: userID,
	}
}

func (a *CheckInAPI) GetDailyList(ctx context.Context) (*DailyListData, error) {
	ts := time.Now().Unix()

	formData := map[string]string{
		"data": fmt.Sprintf("%d", time.Now().Year()),
	}

	logger.Info("获取每日任务列表", "year", time.Now().Year())

	resp, err := a.client.PostFormWithToken(ctx, PathDailyList, formData, ts, AppVersion)
	if err != nil {
		return nil, fmt.Errorf("获取任务列表失败: %w", err)
	}

	plaintext, err := a.client.DecryptResponse(resp, ts)
	if err != nil {
		return nil, fmt.Errorf("解密任务列表失败: %w", err)
	}

	var dailyList DailyListData
	if err := json.Unmarshal([]byte(plaintext), &dailyList); err != nil {
		return nil, fmt.Errorf("解析任务列表失败: %w", err)
	}

	logger.Info("获取任务列表成功", "count", len(dailyList.Tasks))

	return &dailyList, nil
}

func (a *CheckInAPI) DailyCheckIn(ctx context.Context, dailyID string) (*DailyChkData, error) {
	ts := time.Now().Unix()

	formData := map[string]string{
		"user_id":  a.userID,
		"daily_id": dailyID,
	}

	logger.Info("执行签到", "user_id", a.userID, "daily_id", dailyID)

	resp, err := a.client.PostFormWithToken(ctx, PathDailyChk, formData, ts, AppVersion)
	if err != nil {
		return nil, fmt.Errorf("签到请求失败: %w", err)
	}

	plaintext, err := a.client.DecryptResponse(resp, ts)
	if err != nil {
		return nil, fmt.Errorf("解密签到响应失败: %w", err)
	}

	var chkData DailyChkData
	if err := json.Unmarshal([]byte(plaintext), &chkData); err != nil {
		return nil, fmt.Errorf("解析签到结果失败: %w", err)
	}

	if chkData.Success {
		logger.Info("签到成功", "message", chkData.Message)
	} else {
		logger.Warn("签到失败", "message", chkData.Message)
	}

	return &chkData, nil
}

func (a *CheckInAPI) PerformCheckIn(ctx context.Context) error {
	dailyList, err := a.GetDailyList(ctx)
	if err != nil {
		return fmt.Errorf("获取任务列表失败: %w", err)
	}

	if len(dailyList.Tasks) == 0 {
		logger.Info("没有可用的签到任务")
		return nil
	}

	task := dailyList.Tasks[0]
	result, err := a.DailyCheckIn(ctx, task.DailyID)
	if err != nil {
		return fmt.Errorf("签到失败: %w", err)
	}

	if !result.Success {
		return fmt.Errorf("签到未成功: %s", result.Message)
	}

	return nil
}
