package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/INKCR0W/jm-automation/pkg/crypto"
	"github.com/INKCR0W/jm-automation/pkg/logger"
	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

// 响应码
const (
	CodeSuccess = 200
	CodeError   = -1
)

// 加密响应结构
type EncryptedResponse struct {
	Code int    `json:"code"`
	Data string `json:"data"`
}

type Client struct {
	httpClient tls_client.HttpClient
	baseURL    string
	timeout    time.Duration
	cookies    []*http.Cookie
	cookieFile string
	userID     string
	username   string
}

type Response struct {
	StatusCode int
	Body       []byte
	Headers    http.Header
}

// CookieData Cookie 持久化数据结构
type CookieData struct {
	Name    string    `json:"name"`
	Value   string    `json:"value"`
	Path    string    `json:"path"`
	Domain  string    `json:"domain"`
	Expires time.Time `json:"expires"`
	MaxAge  int       `json:"max_age"`
}

// SessionData 会话持久化数据结构（包含 cookies 和用户信息）
type SessionData struct {
	Cookies  []CookieData `json:"cookies"`
	UserID   string       `json:"user_id,omitempty"`
	Username string       `json:"username,omitempty"`
}

func New(baseURL string, timeout time.Duration) (*Client, error) {
	// 使用 Chrome 浏览器指纹
	options := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(int(timeout.Seconds())),
		tls_client.WithClientProfile(profiles.Chrome_120),
	}

	httpClient, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), options...)
	if err != nil {
		return nil, fmt.Errorf("创建 HTTP 客户端失败: %w", err)
	}

	// 创建 cookies 目录
	cookieDir := "data/cookies"
	if err := os.MkdirAll(cookieDir, 0755); err != nil {
		logger.Warn("创建 cookies 目录失败", "error", err)
	}

	client := &Client{
		httpClient: httpClient,
		baseURL:    strings.TrimRight(baseURL, "/"),
		timeout:    timeout,
		cookieFile: filepath.Join(cookieDir, "cookies.json"),
	}

	// 尝试加载已保存的 cookies
	if err := client.LoadCookies(); err != nil {
		logger.Debug("加载 cookies 失败", "error", err)
	}

	return client, nil
}

// GetWithToken 发送带 token 的 GET 请求
func (c *Client) GetWithToken(ctx context.Context, path string, ts int64, appVersion string) (*Response, error) {
	token, tokenparam := crypto.TokenAndTokenParam(ts, appVersion)
	headers := map[string]string{
		"token":      token,
		"tokenparam": tokenparam,
	}
	return c.Get(ctx, path, headers)
}

// PostFormWithToken 发送带 token 的 POST 表单请求
func (c *Client) PostFormWithToken(ctx context.Context, path string, formData map[string]string, ts int64, appVersion string) (*Response, error) {
	token, tokenparam := crypto.TokenAndTokenParam(ts, appVersion)
	headers := map[string]string{
		"token":        token,
		"tokenparam":   tokenparam,
		"Content-Type": "application/x-www-form-urlencoded",
	}
	return c.PostForm(ctx, path, formData, headers)
}

// PostForm 发送 POST 表单请求
func (c *Client) PostForm(ctx context.Context, path string, formData map[string]string, headers map[string]string) (*Response, error) {
	// 构建 form data
	values := make([]string, 0, len(formData))
	for k, v := range formData {
		values = append(values, fmt.Sprintf("%s=%s", k, v))
	}
	body := strings.Join(values, "&")

	return c.doRequestRaw(ctx, http.MethodPost, path, body, headers)
}

// DecryptResponse 解密 API 响应
func (c *Client) DecryptResponse(resp *Response, ts int64) (string, error) {
	// 先尝试解析为标准的加密响应
	var encResp EncryptedResponse
	if err := json.Unmarshal(resp.Body, &encResp); err != nil {
		return "", fmt.Errorf("解析加密响应失败: %w", err)
	}

	// 如果返回错误码，直接返回错误（不需要解密）
	if encResp.Code != CodeSuccess {
		// 尝试解析错误消息
		var errorResp struct {
			Code     int    `json:"code"`
			ErrorMsg string `json:"errorMsg"`
		}
		if err := json.Unmarshal(resp.Body, &errorResp); err == nil && errorResp.ErrorMsg != "" {
			return "", fmt.Errorf("API 返回错误 (code=%d): %s", errorResp.Code, errorResp.ErrorMsg)
		}
		return "", fmt.Errorf("API 返回错误码: %d", encResp.Code)
	}

	// 解密数据
	plaintext, err := crypto.DecodeRespData(encResp.Data, ts)
	if err != nil {
		return "", fmt.Errorf("解密响应数据失败: %w", err)
	}

	return plaintext, nil
}

func (c *Client) Get(ctx context.Context, path string, headers map[string]string) (*Response, error) {
	return c.doRequest(ctx, http.MethodGet, path, nil, headers)
}

func (c *Client) Post(ctx context.Context, path string, body interface{}, headers map[string]string) (*Response, error) {
	return c.doRequest(ctx, http.MethodPost, path, body, headers)
}

func (c *Client) doRequestRaw(ctx context.Context, method, path, body string, headers map[string]string) (*Response, error) {
	url := c.baseURL + path

	req, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	// 设置默认 headers
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")

	// 自定义 headers
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	// 添加 cookies
	for _, cookie := range c.cookies {
		req.AddCookie(cookie)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 保存 cookies
	if cookies := resp.Cookies(); len(cookies) > 0 {
		c.cookies = append(c.cookies, cookies...)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	return &Response{
		StatusCode: resp.StatusCode,
		Body:       respBody,
		Headers:    resp.Header,
	}, nil
}

func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}, headers map[string]string) (*Response, error) {
	url := c.baseURL + path

	var bodyReader io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("序列化请求体失败: %w", err)
		}
		bodyReader = strings.NewReader(string(jsonData))
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	// 设置默认 headers
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	// 自定义 headers
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	// 添加 cookies
	for _, cookie := range c.cookies {
		req.AddCookie(cookie)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 保存 cookies
	if cookies := resp.Cookies(); len(cookies) > 0 {
		c.cookies = append(c.cookies, cookies...)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	return &Response{
		StatusCode: resp.StatusCode,
		Body:       respBody,
		Headers:    resp.Header,
	}, nil
}

func (c *Client) SetCookies(cookies []*http.Cookie) {
	c.cookies = cookies
	// 自动保存 cookies
	if err := c.SaveCookies(); err != nil {
		logger.Warn("保存 cookies 失败", "error", err)
	}
}

func (c *Client) GetCookies() []*http.Cookie {
	return c.cookies
}

// SaveCookies 保存 cookies 到文件
func (c *Client) SaveCookies() error {
	if len(c.cookies) == 0 {
		return nil
	}

	// 转换为可序列化的格式
	cookieData := make([]CookieData, 0, len(c.cookies))
	for _, cookie := range c.cookies {
		cookieData = append(cookieData, CookieData{
			Name:    cookie.Name,
			Value:   cookie.Value,
			Path:    cookie.Path,
			Domain:  cookie.Domain,
			Expires: cookie.Expires,
			MaxAge:  cookie.MaxAge,
		})
	}

	// 保存会话数据（包含 cookies 和用户信息）
	sessionData := SessionData{
		Cookies:  cookieData,
		UserID:   c.userID,
		Username: c.username,
	}

	data, err := json.MarshalIndent(sessionData, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化会话数据失败: %w", err)
	}

	if err := os.WriteFile(c.cookieFile, data, 0600); err != nil {
		return fmt.Errorf("写入会话文件失败: %w", err)
	}

	return nil
}

// LoadCookies 从文件加载 cookies
func (c *Client) LoadCookies() error {
	data, err := os.ReadFile(c.cookieFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 文件不存在不算错误
		}
		return fmt.Errorf("读取会话文件失败: %w", err)
	}

	// 尝试解析为新格式（SessionData）
	var sessionData SessionData
	if err := json.Unmarshal(data, &sessionData); err == nil && len(sessionData.Cookies) > 0 {
		// 新格式
		now := time.Now()
		validCookies := make([]*http.Cookie, 0)
		for _, cd := range sessionData.Cookies {
			// 检查是否过期
			if !cd.Expires.IsZero() && cd.Expires.Before(now) {
				continue
			}

			validCookies = append(validCookies, &http.Cookie{
				Name:    cd.Name,
				Value:   cd.Value,
				Path:    cd.Path,
				Domain:  cd.Domain,
				Expires: cd.Expires,
				MaxAge:  cd.MaxAge,
			})
		}

		if len(validCookies) > 0 {
			c.cookies = validCookies
			c.userID = sessionData.UserID
			c.username = sessionData.Username
			logger.Info("加载会话成功", "count", len(validCookies), "user_id", c.userID)
		}

		return nil
	}

	// 尝试解析为旧格式（[]CookieData）以保持向后兼容
	var cookieData []CookieData
	if err := json.Unmarshal(data, &cookieData); err != nil {
		return fmt.Errorf("解析会话文件失败: %w", err)
	}

	// 转换回 http.Cookie 并过滤过期的
	now := time.Now()
	validCookies := make([]*http.Cookie, 0)
	for _, cd := range cookieData {
		// 检查是否过期
		if !cd.Expires.IsZero() && cd.Expires.Before(now) {
			continue
		}

		validCookies = append(validCookies, &http.Cookie{
			Name:    cd.Name,
			Value:   cd.Value,
			Path:    cd.Path,
			Domain:  cd.Domain,
			Expires: cd.Expires,
			MaxAge:  cd.MaxAge,
		})
	}

	if len(validCookies) > 0 {
		c.cookies = validCookies
		logger.Info("加载 cookies 成功（旧格式）", "count", len(validCookies))
	}

	return nil
}

// HasValidCookies 检查是否有有效的登录 cookies
func (c *Client) HasValidCookies() bool {
	now := time.Now()
	for _, cookie := range c.cookies {
		// 检查是否有 remember 或 AVS cookie 且未过期
		if cookie.Name == "remember" || cookie.Name == "AVS" {
			if cookie.Expires.IsZero() || cookie.Expires.After(now) {
				return true
			}
		}
	}
	return false
}

// SetUserInfo 设置用户信息（用于缓存）
func (c *Client) SetUserInfo(userID, username string) {
	c.userID = userID
	c.username = username
	// 自动保存会话信息
	if err := c.SaveCookies(); err != nil {
		logger.Warn("保存会话信息失败", "error", err)
	}
}

// GetUserID 获取缓存的用户 ID
func (c *Client) GetUserID() string {
	return c.userID
}

// GetUsername 获取缓存的用户名
func (c *Client) GetUsername() string {
	return c.username
}
