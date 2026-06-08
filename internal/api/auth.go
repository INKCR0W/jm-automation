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
	if loginData, ok := a.tryUseCachedSession(ctx); ok {
		return loginData, nil
	}

	loginData, err := a.loginWithCredentials(ctx, username, password)
	if err != nil {
		return nil, err
	}

	a.applyLoginSession(*loginData)

	logger.Info("登录成功", "uid", loginData.UID, "username", loginData.Username, "level", loginData.Level)

	return loginData, nil
}

func (a *AuthAPI) tryUseCachedSession(ctx context.Context) (*LoginData, bool) {
	if !a.client.HasValidCookies() {
		return nil, false
	}

	logger.Info("检测到有效的登录状态，尝试使用已保存的 cookies")

	cachedUserID := a.client.GetUserID()
	cachedUsername := a.client.GetUsername()
	if cachedUserID == "" || cachedUsername == "" {
		logger.Info("缺少缓存的用户信息，重新登录")
		return nil, false
	}

	// 尝试调用一个需要登录的轻量级接口来验证登录状态。
	ts := time.Now().UnixMilli()
	resp, err := a.client.GetWithToken(ctx, "/favorite?page=1&folder_id=0&o=mr", ts, AppVersion)
	if err == nil && resp.StatusCode == 200 {
		if _, err := a.client.DecryptResponse(resp, ts); err == nil {
			a.avs = findCookieValue(a.client.GetCookies(), "AVS")
			a.userID = cachedUserID
			logger.Info("使用已保存的登录状态成功", "uid", cachedUserID, "username", cachedUsername)

			return &LoginData{
				UID:      cachedUserID,
				Username: cachedUsername,
			}, true
		}
	}

	logger.Info("已保存的登录状态无效，重新登录")
	return nil, false
}

func (a *AuthAPI) loginWithCredentials(ctx context.Context, username, password string) (*LoginData, error) {
	ts := time.Now().UnixMilli()

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

	loginData, err := parseLoginResponse(plaintext, username)
	if err != nil {
		return nil, err
	}

	return &loginData, nil
}

func parseLoginResponse(plaintext, fallbackUsername string) (LoginData, error) {
	var loginData LoginData
	if err := json.Unmarshal([]byte(plaintext), &loginData); err != nil {
		return LoginData{}, fmt.Errorf("解析登录数据失败: %w", err)
	}

	if loginData.UID == "" {
		return LoginData{}, fmt.Errorf("登录响应中缺少用户ID")
	}
	if loginData.Username == "" {
		loginData.Username = loginData.Nickname
	}
	if loginData.Username == "" {
		loginData.Username = fallbackUsername
	}

	return loginData, nil
}

func (a *AuthAPI) applyLoginSession(loginData LoginData) {
	a.avs = loginData.S
	a.userID = loginData.UID

	cookie := &http.Cookie{
		Name:  "AVS",
		Value: a.avs,
	}

	allCookies := a.client.GetCookies()
	allCookies = append(allCookies, cookie)
	a.client.SetCookies(allCookies)

	if loginData.JWTToken != "" {
		a.client.SetJWTToken(loginData.JWTToken)
	}

	a.client.SetUserInfo(loginData.UID, loginData.Username)
}

func findCookieValue(cookies []*http.Cookie, name string) string {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

func (a *AuthAPI) GetUserID() string {
	return a.userID
}

func (a *AuthAPI) GetAVS() string {
	return a.avs
}
