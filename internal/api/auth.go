package api

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/INKCR0W/jm-automation/internal/client"
	"github.com/INKCR0W/jm-automation/pkg/logger"
	http "github.com/bogdanfinn/fhttp"
)

type AuthAPI struct {
	client *client.Client
	avs    string
	userID string
}

func NewAuthAPI(c *client.Client) *AuthAPI {
	return &AuthAPI{client: c}
}

// Login 登录接口，如果已有有效的 cookies 则跳过登录
func (a *AuthAPI) Login(ctx context.Context, username, password string) (*LoginData, error) {
	// 检查是否已有有效的登录状态
	if a.client.HasValidCookies() {
		logger.Info("检测到有效的登录状态，尝试使用已保存的 cookies")

		// 尝试调用一个需要登录的接口来验证登录状态
		// 使用获取收藏夹接口作为验证（轻量级接口）
		ts := time.Now().Unix()
		resp, err := a.client.GetWithToken(ctx, "/favorite?page=1&folder_id=0&o=mr", ts, AppVersion)
		if err == nil && resp.StatusCode == 200 {
			// 尝试解密响应，如果成功说明登录状态有效
			plaintext, err := a.client.DecryptResponse(resp, ts)
			if err == nil {
				logger.Info("使用已保存的登录状态成功")

				// 从 cookies 中提取 AVS
				for _, cookie := range a.client.GetCookies() {
					if cookie.Name == "AVS" {
						a.avs = cookie.Value
						break
					}
				}

				// 尝试从收藏夹响应中提取用户信息
				var favoriteData map[string]interface{}
				if err := json.Unmarshal([]byte(plaintext), &favoriteData); err == nil {
					// 从收藏夹数据中提取用户ID（如果有的话）
					if list, ok := favoriteData["list"].([]interface{}); ok && len(list) > 0 {
						// 收藏夹有数据，说明登录有效
						a.userID = "cached_user"
					}
				}

				// 返回一个简单的登录数据
				return &LoginData{
					UID:      "cached",
					Username: username,
				}, nil
			}
		}

		logger.Info("已保存的登录状态无效，重新登录")
	}

	ts := time.Now().Unix()

	formData := map[string]string{
		"username":       username,
		"password":       password,
		"login_remember": "on",
		"id_remember":    "on",
		"submit_login":   "1",
	}

	logger.Info("开始登录", "username", username)

	resp, err := a.client.PostFormWithToken(ctx, PathLogin, formData, ts, AppVersion)
	if err != nil {
		return nil, fmt.Errorf("登录请求失败: %w", err)
	}

	plaintext, err := a.client.DecryptResponse(resp, ts)
	if err != nil {
		return nil, fmt.Errorf("解密登录响应失败: %w", err)
	}

	var loginData LoginData
	if err := json.Unmarshal([]byte(plaintext), &loginData); err != nil {
		return nil, fmt.Errorf("解析登录数据失败: %w", err)
	}

	a.avs = loginData.S
	a.userID = loginData.UID

	cookie := &http.Cookie{
		Name:  "AVS",
		Value: a.avs,
	}

	allCookies := a.client.GetCookies()
	allCookies = append(allCookies, cookie)
	a.client.SetCookies(allCookies)

	logger.Info("登录成功", "uid", loginData.UID, "username", loginData.Username, "level", loginData.Level, "cookies", len(allCookies))

	return &loginData, nil
}

func (a *AuthAPI) GetUserID() string {
	return a.userID
}

func (a *AuthAPI) GetAVS() string {
	return a.avs
}
