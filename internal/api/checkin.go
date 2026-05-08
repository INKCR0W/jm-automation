package api

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/INKCR0W/jm-automation/internal/client"
	"github.com/INKCR0W/jm-automation/pkg/logger"
	"github.com/INKCR0W/jm-automation/pkg/utils"
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
	// 验证 userID 不为空
	if a.userID == "" {
		return nil, fmt.Errorf("用户ID为空，无法执行签到操作")
	}

	ts := time.Now().UnixMilli()

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

	logger.Info("获取任务列表成功", "count", len(dailyList.List))

	return &dailyList, nil
}

func (a *CheckInAPI) DailyCheckIn(ctx context.Context, dailyID string) (*DailyChkData, error) {
	ts := time.Now().UnixMilli()

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

	// 如果返回空字符串，可能是已经签到过了或签到成功但无返回数据
	if plaintext == "" || plaintext == "{}" {
		logger.Info("签到完成（空响应，可能已签到或签到成功）")
		return &DailyChkData{Success: true, Message: "签到完成", Msg: ""}, nil
	}

	var chkData DailyChkData
	if err := json.Unmarshal([]byte(plaintext), &chkData); err != nil {
		return nil, fmt.Errorf("解析签到结果失败: %w (原始数据: %s)", err, plaintext)
	}

	// 检查是否已经签到过（参考 Breeze 实现）
	if chkData.Msg == "今天已经签到过了" {
		logger.Info("今天已经签到过了")
		return &DailyChkData{Success: true, Message: "今天已经签到过了", Msg: chkData.Msg}, nil
	}

	// 如果有 msg 字段且不为空，说明签到成功
	if chkData.Msg != "" {
		logger.Info("签到成功", "msg", chkData.Msg)
		return &DailyChkData{Success: true, Message: chkData.Msg, Msg: chkData.Msg}, nil
	}

	// 兼容其他可能的响应格式
	if chkData.Success {
		logger.Info("签到成功", "message", chkData.Message)
	} else {
		logger.Warn("签到失败", "message", chkData.Message)
	}

	return &chkData, nil
}

func (a *CheckInAPI) PerformCheckIn(ctx context.Context) error {
	const maxRetries = 3
	var dailyList *DailyListData
	var err error

	for retryCount := 1; retryCount <= maxRetries; retryCount++ {
		dailyList, err = a.GetDailyList(ctx)
		if err == nil {
			break
		}

		logger.Error("获取任务列表失败", "retry", retryCount, "max_retries", maxRetries, "error", err)
		if retryCount < maxRetries {
			backoff := time.Duration(5<<(retryCount-1)) * time.Second
			logger.Info("等待重试", "backoff", backoff)
			time.Sleep(backoff)
			continue
		}
		return fmt.Errorf("获取任务列表失败（已重试%d次）: %w", maxRetries, err)
	}

	if len(dailyList.List) == 0 {
		logger.Info("没有可用的签到任务")
		return nil
	}

	lastTask := dailyList.List[len(dailyList.List)-1]

	if lastTask.ID == "" {
		return fmt.Errorf("任务ID为空，无法执行签到")
	}

	logger.Info("准备签到", "task_id", lastTask.ID, "year", lastTask.Year, "month", lastTask.Month)

	utils.RandomDelay(500*time.Millisecond, 3*time.Second)

	for retryCount := 1; retryCount <= maxRetries; retryCount++ {
		result, err := a.DailyCheckIn(ctx, lastTask.ID)
		if err == nil {
			if !result.Success {
				return fmt.Errorf("签到未成功: %s", result.Message)
			}
			return nil
		}

		logger.Error("签到失败", "retry", retryCount, "max_retries", maxRetries, "error", err)
		if retryCount < maxRetries {
			backoff := time.Duration(1<<(retryCount-1)) * time.Second
			logger.Info("等待重试", "backoff", backoff)
			time.Sleep(backoff)
			continue
		}
		return fmt.Errorf("签到失败（已重试%d次）: %w", maxRetries, err)
	}

	return nil
}
