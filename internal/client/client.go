package client

import (
	"context"
	"encoding/json"
	"errors"
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

// 上游 header 超过 8K 左右就直接返回 400 HTML，正常会话才几百字节，这里留够余量兜底
const maxCookieHeaderBytes = 4096

// 加密响应结构
type EncryptedResponse struct {
	Code int    `json:"code"`
	Data string `json:"data"`
}

type Client struct {
	httpClient interface {
		Do(*http.Request) (*http.Response, error)
	}

	baseURL         string
	baseURLs        []string
	baseURLRevision uint64

	timeout    time.Duration
	cookies    []sessionCookie
	saveMu     sync.Mutex
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
	RequestURL *url.URL

	baseURLSelection baseURLSelection
}

type baseURLSelection struct {
	client   *Client
	baseURL  string
	revision uint64
}

type baseURLConfirmation uint8

const (
	confirmHTTPStatus baseURLConfirmation = iota
	confirmAPIResult
)

// CookieData Cookie 持久化数据结构
type CookieData struct {
	Name        string    `json:"name"`
	Value       string    `json:"value"`
	Path        string    `json:"path"`
	Domain      string    `json:"domain"`
	HostOnly    string    `json:"host_only,omitempty"`
	Expires     time.Time `json:"expires"`
	MaxAge      int       `json:"max_age"`
	Secure      bool      `json:"secure,omitempty"`
	HTTPOnly    bool      `json:"http_only,omitempty"`
	LoginOrigin string    `json:"login_origin,omitempty"`
}

// SessionData 会话持久化数据结构（包含 cookies 和用户信息）
type SessionData struct {
	Cookies  []CookieData `json:"cookies"`
	UserID   string       `json:"user_id,omitempty"`
	Username string       `json:"username,omitempty"`
	JWTToken string       `json:"jwt_token,omitempty"`
}

func New(baseURL string, timeout time.Duration, username string) (*Client, error) {
	client, err := NewEphemeral(baseURL, timeout)
	if err != nil {
		return nil, err
	}

	cookieDir := "data/cookies"
	if err := os.MkdirAll(cookieDir, 0755); err != nil {
		logger.Warn("创建 cookies 目录失败", "error", err)
	}

	cookieFileName := "cookies.json"
	if username != "" {
		cookieFileName = username + "_session.json"
	}

	client.cookieFile = filepath.Join(cookieDir, cookieFileName)

	if err := client.LoadCookies(); err != nil {
		logger.Debug("加载 cookies 失败", "username", username, "error", err)
	}

	return client, nil
}

// 不落盘的临时 client，探活得跟业务请求同一套 TLS 指纹，否则探通了照样可能被拦
func NewEphemeral(baseURL string, timeout time.Duration) (*Client, error) {
	client := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		timeout: timeout,
	}
	httpClient, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(),
		tls_client.WithTimeoutSeconds(int(timeout.Seconds())),
		tls_client.WithClientProfile(profiles.Chrome_144),
		tls_client.WithCookieJar(sessionCookieJar{client}),
		tls_client.WithCustomRedirectFunc(checkRedirect),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 HTTP 客户端失败: %w", err)
	}

	client.httpClient = httpClient
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
	c.baseURLRevision++
}

func (c *Client) BaseURL() string {
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()

	return c.baseURL
}

// ConfirmBaseURL 在业务调用确认响应成功后将产生它的备用候选设为首选
func (c *Client) ConfirmBaseURL(resp *Response) {
	selection := resp.baseURLSelection
	if selection.client != c {
		return
	}

	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()

	// 更新候选或首选后，旧请求的结果不能再次覆盖首选
	if selection.revision == c.baseURLRevision {
		c.baseURL = selection.baseURL
		c.baseURLRevision++
	}
}

// GetWithToken 发送带 token 的 GET 请求，调用方验证业务结果后可确认首选域名
func (c *Client) GetWithToken(ctx context.Context, path string, ts int64, appVersion string) (*Response, error) {
	token, tokenparam := crypto.TokenAndTokenParam(ts, appVersion)
	headers := map[string]string{
		"token":      token,
		"tokenparam": tokenparam,
	}
	return c.doRequest(ctx, http.MethodGet, path, nil, headers, confirmAPIResult)
}

// PostFormWithToken 发送带 token 的 POST 表单请求，调用方验证业务结果后可确认首选域名
func (c *Client) PostFormWithToken(ctx context.Context, path string, formData map[string]string, ts int64, appVersion string) (*Response, error) {
	token, tokenparam := crypto.TokenAndTokenParam(ts, appVersion)
	headers := map[string]string{
		"token":        token,
		"tokenparam":   tokenparam,
		"Content-Type": "application/x-www-form-urlencoded",
	}
	return c.postForm(ctx, path, formData, headers, confirmAPIResult)
}

// PostForm 发送 POST 表单请求
func (c *Client) PostForm(ctx context.Context, path string, formData map[string]string, headers map[string]string) (*Response, error) {
	return c.postForm(ctx, path, formData, headers, confirmHTTPStatus)
}

func (c *Client) postForm(ctx context.Context, path string, formData map[string]string, headers map[string]string, confirmation baseURLConfirmation) (*Response, error) {
	// 构建 form data
	values := url.Values{}
	for k, v := range formData {
		values.Set(k, v)
	}
	body := values.Encode()

	return c.doWithBaseURLFallback(http.MethodPost, confirmation, func(baseURL string) (*Response, error) {
		return c.doRequestRawOnce(ctx, http.MethodPost, baseURL, path, body, headers)
	})
}

// DecryptResponse 解密 API 响应
func (c *Client) DecryptResponse(resp *Response, ts int64) (string, error) {
	return decryptResponse(resp, ts, false)
}

// DecryptResponseWithOptionalData 允许成功响应不包含加密数据
func (c *Client) DecryptResponseWithOptionalData(resp *Response, ts int64) (string, error) {
	return decryptResponse(resp, ts, true)
}

func decryptResponse(resp *Response, ts int64, allowEmptyData bool) (string, error) {
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

	if allowEmptyData && encResp.Data == "" {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", fmt.Errorf("服务器返回 HTTP 错误 (status=%d)", resp.StatusCode)
		}
		return "", nil
	}

	// 解密数据
	plaintext, err := crypto.DecodeRespDataWithSeeds(encResp.Data, ts, crypto.AppDataSecret, crypto.AppTokenSecret2)
	if err != nil {
		return "", fmt.Errorf("解密响应数据失败: %w", err)
	}

	return plaintext, nil
}

func (c *Client) Get(ctx context.Context, path string, headers map[string]string) (*Response, error) {
	return c.doRequest(ctx, http.MethodGet, path, nil, headers, confirmHTTPStatus)
}

func (c *Client) Post(ctx context.Context, path string, body interface{}, headers map[string]string) (*Response, error) {
	return c.doRequest(ctx, http.MethodPost, path, body, headers, confirmHTTPStatus)
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

	// Cookie 统一从会话 Jar 按目标 URL 选取
	req.Header.Del("Cookie")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			logger.Debug("关闭响应体失败", "error", closeErr)
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	var requestURL *url.URL
	if resp.Request != nil {
		requestURL = resp.Request.URL
	}
	return &Response{
		StatusCode: resp.StatusCode,
		Body:       respBody,
		Headers:    resp.Header,
		RequestURL: requestURL,
	}, nil
}

func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}, headers map[string]string, confirmation baseURLConfirmation) (*Response, error) {
	return c.doWithBaseURLFallback(method, confirmation, func(baseURL string) (*Response, error) {
		return c.doRequestOnce(ctx, method, baseURL, path, body, headers)
	})
}

func (c *Client) doWithBaseURLFallback(method string, confirmation baseURLConfirmation, execute func(baseURL string) (*Response, error)) (*Response, error) {
	baseURLs, revision := c.requestBaseURLs()
	var lastResp *Response
	var lastErr error

	for i, baseURL := range baseURLs {
		resp, err := execute(baseURL)
		if err != nil {
			lastErr = err
			continue
		}
		if method == http.MethodGet && isGatewayFailure(resp.StatusCode) {
			logger.Debug("API 网关暂不可用", "status", resp.StatusCode, "base_url", baseURL)
		} else if !looksLikeHTML(resp) {
			if i > 0 && resp.StatusCode >= 200 && resp.StatusCode < 300 {
				resp.baseURLSelection = baseURLSelection{client: c, baseURL: baseURL, revision: revision}
				if confirmation == confirmHTTPStatus {
					c.ConfirmBaseURL(resp)
				}
			}
			return resp, nil
		}
		lastResp = resp
	}

	if lastResp != nil {
		return lastResp, nil
	}
	return nil, lastErr
}

func isGatewayFailure(status int) bool {
	switch status {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
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

func (c *Client) requestBaseURLs() ([]string, uint64) {
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()

	if len(c.baseURLs) == 0 {
		return []string{c.baseURL}, c.baseURLRevision
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
	return out, c.baseURLRevision
}

func looksLikeHTML(resp *Response) bool {
	body := strings.TrimSpace(string(resp.Body))
	contentType := strings.ToLower(resp.Headers.Get("Content-Type"))
	return strings.HasPrefix(body, "<") || strings.Contains(contentType, "text/html")
}

// SaveCookies 保存 cookies 到文件
func (c *Client) SaveCookies() error {
	if c.cookieFile == "" {
		return nil
	}

	c.saveMu.Lock()
	defer c.saveMu.Unlock()

	snapshot := c.sessionSnapshot()

	// 转换为可序列化的格式
	cookieData := make([]CookieData, 0, len(snapshot.cookies))
	for _, cookie := range snapshot.cookies {
		cookieData = append(cookieData, CookieData{
			Name:        cookie.Name,
			Value:       cookie.Value,
			Path:        cookie.Path,
			Domain:      cookie.Domain,
			HostOnly:    cookie.hostOnly,
			LoginOrigin: cookie.loginOrigin.cacheKey(),
			Expires:     cookie.Expires,
			MaxAge:      cookie.MaxAge,
			Secure:      cookie.Secure,
			HTTPOnly:    cookie.HttpOnly,
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

	if err := writeSessionFileAtomically(c.cookieFile, data); err != nil {
		return fmt.Errorf("写入会话文件失败: %w", err)
	}

	return nil
}

func writeSessionFileAtomically(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".session-*")
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	err = errors.Join(writeErr, file.Close())
	if err == nil {
		err = os.Rename(file.Name(), path)
		if err == nil {
			return nil
		}
	}
	if cleanupErr := os.Remove(file.Name()); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
		return errors.Join(err, fmt.Errorf("清理临时会话文件失败: %w", cleanupErr))
	}
	return err
}

// LoadCookies 从文件加载 cookies
func (c *Client) LoadCookies() error {
	data, err := c.readSessionFile()
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}

	now := time.Now()
	policy := c.cookieValidationPolicy()

	// 尝试解析为新格式（SessionData）
	if sessionData, ok := parseSessionData(data); ok {
		validCookies := filterValidCookies(sessionData.Cookies, now, policy)
		c.applySession(validCookies, sessionData.UserID, sessionData.Username, sessionData.JWTToken)
		logger.Info("加载会话成功", "count", len(validCookies), "user_id", sessionData.UserID, "has_jwt", sessionData.JWTToken != "")

		return nil
	}

	// 尝试解析为旧格式（[]CookieData）以保持向后兼容
	cookieData, err := parseLegacyCookieData(data)
	if err != nil {
		return fmt.Errorf("解析会话文件失败: %w", err)
	}

	validCookies := filterValidCookies(cookieData, now, policy)
	c.replaceCookies(validCookies)
	if len(validCookies) > 0 {
		logger.Info("加载 cookies 成功（旧格式）", "count", len(validCookies))
	}

	return nil
}

func (c *Client) readSessionFile() ([]byte, error) {
	if c.cookieFile == "" {
		return nil, nil
	}

	data, err := os.ReadFile(c.cookieFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // 文件不存在不算错误
		}
		return nil, fmt.Errorf("读取会话文件失败: %w", err)
	}
	return data, nil
}

func parseSessionData(data []byte) (SessionData, bool) {
	var sessionData SessionData
	if err := json.Unmarshal(data, &sessionData); err != nil || !sessionData.hasData() {
		return SessionData{}, false
	}
	return sessionData, true
}

func parseLegacyCookieData(data []byte) ([]CookieData, error) {
	var cookieData []CookieData
	if err := json.Unmarshal(data, &cookieData); err != nil {
		return nil, err
	}
	return cookieData, nil
}

func parseBaseURL(raw string) (*url.URL, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, false
	}
	if !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
		value = "https://" + value
	}
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" {
		return nil, false
	}
	return u, true
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
	return s.Cookies != nil || s.UserID != "" || s.Username != "" || s.JWTToken != ""
}

type sessionSnapshot struct {
	cookies  []sessionCookie
	userID   string
	username string
	jwtToken string
}

func (c *Client) sessionSnapshot() sessionSnapshot {
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()

	return sessionSnapshot{
		cookies:  append([]sessionCookie(nil), c.cookies...),
		userID:   c.userID,
		username: c.username,
		jwtToken: c.jwtToken,
	}
}

func (c *Client) getJWTTokenSnapshot() string {
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()

	return c.jwtToken
}

func (c *Client) applySession(cookies []sessionCookie, userID, username, jwtToken string) {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()

	c.cookies = cookies
	c.userID = userID
	c.username = username
	c.jwtToken = jwtToken
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
