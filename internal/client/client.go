package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	baseURLs   []string
	timeout    time.Duration
	cookies    []*http.Cookie
	sessionMu  sync.RWMutex // 保护 cookies、baseURL、JWT 和用户信息等会话状态的并发访问
	cookieFile string
	userID     string
	username   string
	jwtToken   string
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
	JWTToken string       `json:"jwt_token,omitempty"`
}

func New(baseURL string, timeout time.Duration, username string) (*Client, error) {
	options := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(int(timeout.Seconds())),
		tls_client.WithClientProfile(profiles.Chrome_144),
	}

	httpClient, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), options...)
	if err != nil {
		return nil, fmt.Errorf("创建 HTTP 客户端失败: %w", err)
	}

	cookieDir := "data/cookies"
	if err := os.MkdirAll(cookieDir, 0755); err != nil {
		logger.Warn("创建 cookies 目录失败", "error", err)
	}

	cookieFileName := "cookies.json"
	if username != "" {
		cookieFileName = username + "_session.json"
	}

	client := &Client{
		httpClient: httpClient,
		baseURL:    strings.TrimRight(baseURL, "/"),
		timeout:    timeout,
		cookieFile: filepath.Join(cookieDir, cookieFileName),
	}

	if err := client.LoadCookies(); err != nil {
		logger.Debug("加载 cookies 失败", "username", username, "error", err)
	}

	return client, nil
}

func (c *Client) SetBaseURLs(baseURLs []string) {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()

	c.baseURLs = make([]string, 0, len(baseURLs))
	for _, baseURL := range baseURLs {
		baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
		if baseURL != "" {
			c.baseURLs = append(c.baseURLs, baseURL)
		}
	}
	if len(c.baseURLs) > 0 {
		c.baseURL = c.baseURLs[0]
	}
}

func (c *Client) BaseURL() string {
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()

	return c.baseURL
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
	values := url.Values{}
	for k, v := range formData {
		values.Set(k, v)
	}
	body := values.Encode()

	return c.doRequestRaw(ctx, http.MethodPost, path, body, headers)
}

// DecryptResponse 解密 API 响应
func (c *Client) DecryptResponse(resp *Response, ts int64) (string, error) {
	bodyText := strings.TrimSpace(string(resp.Body))
	if strings.HasPrefix(bodyText, "<") {
		return "", fmt.Errorf("服务器返回 HTML，可能是域名失效或请求特征被拦截 (status=%d, content_type=%s)", resp.StatusCode, resp.Headers.Get("Content-Type"))
	}

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
	plaintext, err := crypto.DecodeRespDataWithSeeds(encResp.Data, ts, crypto.AppDataSecret, crypto.AppTokenSecret2)
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
	return c.doWithBaseURLFallback(func(baseURL string) (*Response, error) {
		return c.doRequestRawOnce(ctx, method, baseURL, path, body, headers)
	})
}

func (c *Client) doRequestRawOnce(ctx context.Context, method, baseURL, path, body string, headers map[string]string) (*Response, error) {
	return c.executeRequestOnce(ctx, requestOptions{
		method:  method,
		url:     baseURL + path,
		body:    strings.NewReader(body),
		headers: headers,
	})
}

type requestOptions struct {
	method             string
	url                string
	body               io.Reader
	headers            map[string]string
	defaultContentType string
}

func (c *Client) executeRequestOnce(ctx context.Context, opts requestOptions) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, opts.method, opts.url, opts.body)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	// 设置默认 headers（与 Chrome 144 TLS Profile 匹配）
	// 使用 Chrome 浏览器的标准 header 顺序
	req.Header = http.Header{
		"accept":          {"application/json, text/plain, */*"},
		"accept-encoding": {"gzip, deflate, br"},
		"accept-language": {"zh-CN,zh;q=0.9,en;q=0.8"},
		"user-agent":      {"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/144.0.0.0 Safari/537.36"},
		http.HeaderOrderKey: {
			"accept",
			"accept-encoding",
			"accept-language",
			"user-agent",
		},
	}
	if opts.defaultContentType != "" {
		req.Header.Set("Content-Type", opts.defaultContentType)
	}

	// 自定义 headers
	for k, v := range opts.headers {
		req.Header.Set(k, v)
	}
	if jwtToken := c.getJWTTokenSnapshot(); jwtToken != "" {
		req.Header.Set("Authorization", "Bearer "+jwtToken)
	}

	// 添加 cookies
	for _, cookie := range c.cookieSnapshot() {
		req.AddCookie(cookie)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			logger.Debug("关闭响应体失败", "error", closeErr)
		}
	}()

	// 保存 cookies
	if cookies := resp.Cookies(); len(cookies) > 0 {
		c.appendCookies(cookies)
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
	return c.doWithBaseURLFallback(func(baseURL string) (*Response, error) {
		return c.doRequestOnce(ctx, method, baseURL, path, body, headers)
	})
}

func (c *Client) doWithBaseURLFallback(execute func(baseURL string) (*Response, error)) (*Response, error) {
	baseURLs := c.requestBaseURLs()
	var lastResp *Response
	var lastErr error

	for _, baseURL := range baseURLs {
		resp, err := execute(baseURL)
		if err != nil {
			lastErr = err
			continue
		}
		if !looksLikeHTML(resp) {
			c.setBaseURL(baseURL)
			return resp, nil
		}
		lastResp = resp
		lastErr = fmt.Errorf("服务器返回 HTML (status=%d, base_url=%s)", resp.StatusCode, baseURL)
	}

	if lastResp != nil {
		return lastResp, nil
	}
	return nil, lastErr
}

func (c *Client) doRequestOnce(ctx context.Context, method, baseURL, path string, body interface{}, headers map[string]string) (*Response, error) {
	var bodyReader io.Reader
	var defaultContentType string
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("序列化请求体失败: %w", err)
		}
		bodyReader = strings.NewReader(string(jsonData))
		defaultContentType = "application/json"
	}

	return c.executeRequestOnce(ctx, requestOptions{
		method:             method,
		url:                baseURL + path,
		body:               bodyReader,
		headers:            headers,
		defaultContentType: defaultContentType,
	})
}

func (c *Client) requestBaseURLs() []string {
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()

	if len(c.baseURLs) == 0 {
		return []string{c.baseURL}
	}

	out := make([]string, 0, len(c.baseURLs))
	if c.baseURL != "" {
		out = append(out, c.baseURL)
	}
	for _, baseURL := range c.baseURLs {
		if baseURL != c.baseURL {
			out = append(out, baseURL)
		}
	}
	return out
}

func looksLikeHTML(resp *Response) bool {
	body := strings.TrimSpace(string(resp.Body))
	contentType := strings.ToLower(resp.Headers.Get("Content-Type"))
	return strings.HasPrefix(body, "<") || strings.Contains(contentType, "text/html")
}

func (c *Client) SetCookies(cookies []*http.Cookie) {
	c.sessionMu.Lock()
	c.cookies = cloneCookies(cookies)
	c.sessionMu.Unlock()

	// 自动保存 cookies
	if err := c.SaveCookies(); err != nil {
		logger.Warn("保存 cookies 失败", "error", err)
	}
}

func (c *Client) GetCookies() []*http.Cookie {
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()

	return cloneCookies(c.cookies)
}

// SaveCookies 保存 cookies 到文件
func (c *Client) SaveCookies() error {
	snapshot := c.sessionSnapshot()
	if len(snapshot.cookies) == 0 && snapshot.jwtToken == "" && snapshot.userID == "" && snapshot.username == "" {
		return nil
	}

	// 转换为可序列化的格式
	cookieData := make([]CookieData, 0, len(snapshot.cookies))
	for _, cookie := range snapshot.cookies {
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
		UserID:   snapshot.userID,
		Username: snapshot.username,
		JWTToken: snapshot.jwtToken,
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
	if err := json.Unmarshal(data, &sessionData); err == nil && sessionData.hasData() {
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

		c.applySession(validCookies, sessionData.UserID, sessionData.Username, sessionData.JWTToken)
		logger.Info("加载会话成功", "count", len(validCookies), "user_id", sessionData.UserID, "has_jwt", sessionData.JWTToken != "")

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
		c.setCookies(validCookies)
		logger.Info("加载 cookies 成功（旧格式）", "count", len(validCookies))
	}

	return nil
}

func (c *Client) SetJWTToken(token string) {
	c.sessionMu.Lock()
	c.jwtToken = strings.TrimSpace(token)
	c.sessionMu.Unlock()

	if err := c.SaveCookies(); err != nil {
		logger.Warn("保存 JWT 会话失败", "error", err)
	}
}

func (c *Client) GetJWTToken() string {
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()

	return c.jwtToken
}

// HasValidCookies 检查是否有有效的登录 cookies
func (c *Client) HasValidCookies() bool {
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()

	if c.jwtToken != "" {
		return true
	}

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

func (s *SessionData) hasData() bool {
	return len(s.Cookies) > 0 || s.UserID != "" || s.Username != "" || s.JWTToken != ""
}

type sessionSnapshot struct {
	cookies  []*http.Cookie
	userID   string
	username string
	jwtToken string
}

func (c *Client) sessionSnapshot() sessionSnapshot {
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()

	return sessionSnapshot{
		cookies:  cloneCookies(c.cookies),
		userID:   c.userID,
		username: c.username,
		jwtToken: c.jwtToken,
	}
}

func (c *Client) cookieSnapshot() []*http.Cookie {
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()

	return cloneCookies(c.cookies)
}

func (c *Client) getJWTTokenSnapshot() string {
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()

	return c.jwtToken
}

func (c *Client) setBaseURL(baseURL string) {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()

	c.baseURL = baseURL
}

func (c *Client) setCookies(cookies []*http.Cookie) {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()

	c.cookies = cloneCookies(cookies)
}

func (c *Client) appendCookies(cookies []*http.Cookie) {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()

	c.cookies = append(c.cookies, cloneCookies(cookies)...)
}

func (c *Client) applySession(cookies []*http.Cookie, userID, username, jwtToken string) {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()

	c.cookies = cloneCookies(cookies)
	c.userID = userID
	c.username = username
	c.jwtToken = jwtToken
}

func cloneCookies(cookies []*http.Cookie) []*http.Cookie {
	if len(cookies) == 0 {
		return nil
	}

	cloned := make([]*http.Cookie, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie == nil {
			continue
		}
		cookieCopy := *cookie
		cloned = append(cloned, &cookieCopy)
	}
	return cloned
}

// SetUserInfo 设置用户信息（用于缓存）
func (c *Client) SetUserInfo(userID, username string) {
	c.sessionMu.Lock()
	c.userID = userID
	c.username = username
	c.sessionMu.Unlock()

	// 自动保存会话信息
	if err := c.SaveCookies(); err != nil {
		logger.Warn("保存会话信息失败", "error", err)
	}
}

// GetUserID 获取缓存的用户 ID
func (c *Client) GetUserID() string {
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()

	return c.userID
}

// GetUsername 获取缓存的用户名
func (c *Client) GetUsername() string {
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()

	return c.username
}
