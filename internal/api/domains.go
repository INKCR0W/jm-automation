package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/INKCR0W/jm-automation/internal/client"
	"github.com/INKCR0W/jm-automation/pkg/crypto"
	"github.com/INKCR0W/jm-automation/pkg/logger"
)

type hostConfig struct {
	Server []string `json:"Server"`
	// 每项形如 ["www.example.net", "線路1"]，第一个才是域名，一般是 Server 的超集
	JM3Server [][]string `json:"jm3_Server"`
}

func (c hostConfig) domains() []string {
	domains := make([]string, 0, len(c.Server)+len(c.JM3Server))
	domains = append(domains, c.Server...)
	for _, entry := range c.JM3Server {
		if len(entry) > 0 {
			domains = append(domains, entry[0])
		}
	}
	return domains
}

const settingProbeTimeout = 8 * time.Second

// 探活是串行的，候选一多会把整轮任务的时间预算吃光
const maxProbedCandidates = 5

type Resolution struct {
	BaseURLs []string
	Remote   bool // 远程配置是不是真取到了
	Live     bool // 有没有域名探活通过
}

// 啥都没查出来，只剩内置兜底，这种结果不该覆盖之前解析到的好候选
func (r Resolution) Degraded() bool {
	return !r.Remote && !r.Live
}

// 配置里的域名也要探活，域名不定期下线，失效的钉在第一位会让每个请求先白等一次超时
func ResolveBaseURLs(ctx context.Context, configuredBaseURL string) []string {
	return Resolve(ctx, configuredBaseURL).BaseURLs
}

func Resolve(ctx context.Context, configuredBaseURL string) Resolution {
	dynamic, remote := resolveDynamicBaseURLs(ctx)

	candidates := make([]string, 0, len(dynamic)+1)
	if u := normalizeBaseURL(configuredBaseURL); u != "" {
		candidates = append(candidates, u)
	}
	candidates = append(candidates, dynamic...)
	candidates = uniqueStrings(candidates)

	ordered, live := promoteLiveBaseURL(ctx, candidates)
	return Resolution{BaseURLs: ordered, Remote: remote, Live: live}
}

func ResolveDynamicBaseURLs(ctx context.Context) []string {
	urls, _ := resolveDynamicBaseURLs(ctx)
	return urls
}

// bool 表示远程配置是不是真取到了
func resolveDynamicBaseURLs(ctx context.Context) ([]string, bool) {
	raw, err := fetchHostConfig(ctx)
	if err != nil {
		logger.Warn("获取远程域名配置失败，使用内置域名", "error", err)
		return fallbackBaseURLs(), false
	}

	compact := regexp.MustCompile(`[^A-Za-z0-9+/=]`).ReplaceAllString(raw, "")
	key := crypto.MD5Hex(HostConfigSecret)
	plaintext, err := crypto.DecodeBase64ECBPKCS7(compact, key)
	if err != nil {
		logger.Warn("解密远程域名配置失败，使用内置域名", "error", err)
		return fallbackBaseURLs(), false
	}

	var cfg hostConfig
	if err := json.Unmarshal([]byte(plaintext), &cfg); err != nil {
		logger.Warn("解析远程域名配置失败，使用内置域名", "error", err)
		return fallbackBaseURLs(), false
	}

	domains := cfg.domains()
	urls := make([]string, 0, len(domains)+len(DomainAPIList))
	for _, domain := range domains {
		if u := normalizeBaseURL(domain); u != "" {
			urls = append(urls, u)
		}
	}
	urls = append(urls, fallbackBaseURLs()...)

	return uniqueStrings(urls), true
}

// 探挂的要丢到没试过的后面，否则主域名开始返回 HTML 时，回退会先去撞这些已知的死域名
// 每个白等一次超时，等于把当初要修的卡顿绕回来了
func promoteLiveBaseURL(ctx context.Context, candidates []string) ([]string, bool) {
	for i, baseURL := range candidates {
		if i >= maxProbedCandidates {
			logger.Warn("达到探活上限，剩余候选不再探活",
				"probed", i, "remaining", len(candidates)-i)
			break
		}

		if err := probeSetting(ctx, baseURL); err != nil {
			logger.Warn("API 域名探活失败", "base_url", baseURL, "error", err)
			continue
		}

		logger.Info("API 域名探活成功", "base_url", baseURL)
		if i == 0 {
			return candidates, true
		}

		promoted := make([]string, 0, len(candidates))
		promoted = append(promoted, baseURL)
		promoted = append(promoted, candidates[i+1:]...)
		promoted = append(promoted, candidates[:i]...)
		return promoted, true
	}

	logger.Warn("所有 API 域名探活失败，保持原有候选顺序", "count", len(candidates))
	return candidates, false
}

// 这个接口签名跟业务接口不一样：时间戳用秒，token 用 AppDataSecret 而不是版本号
func probeSetting(ctx context.Context, baseURL string) error {
	tsSec := time.Now().Unix()
	token, tokenparam := crypto.TokenAndTokenParamWithSeed(tsSec, AppVersion, crypto.AppDataSecret)

	probeCtx, cancel := context.WithTimeout(ctx, settingProbeTimeout)
	defer cancel()

	probeClient, err := client.NewEphemeral(baseURL, settingProbeTimeout)
	if err != nil {
		return err
	}

	path := fmt.Sprintf("%s?app_img_shunt=1&t=%d", PathSetting, tsSec)
	resp, err := probeClient.Get(probeCtx, path, map[string]string{
		"token":      token,
		"tokenparam": tokenparam,
	})
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status=%d", resp.StatusCode)
	}

	var encResp EncryptedResponse
	if err := json.Unmarshal(resp.Body, &encResp); err != nil {
		return fmt.Errorf("响应不是预期的加密结构: %w", err)
	}
	if encResp.Data == "" {
		return fmt.Errorf("响应缺少 data 字段 (code=%d)", encResp.Code)
	}
	if _, err := crypto.DecodeRespDataWithSeeds(encResp.Data, tsSec, crypto.AppDataSecret, crypto.AppTokenSecret2); err != nil {
		return fmt.Errorf("解密探活响应失败: %w", err)
	}

	return nil
}

func fetchHostConfig(ctx context.Context) (string, error) {
	httpClient := &http.Client{Timeout: 8 * time.Second}
	var lastErr error

	for _, url := range HostConfigURLs {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("User-Agent", "jm-automation")

		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		if closeErr := resp.Body.Close(); closeErr != nil {
			logger.Debug("关闭远程域名配置响应失败", "error", closeErr)
		}
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("status=%d url=%s", resp.StatusCode, url)
			continue
		}
		return string(body), nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("远程域名配置不可用")
	}
	return "", lastErr
}

func fallbackBaseURLs() []string {
	urls := []string{DefaultBaseURL}
	for _, domain := range DomainAPIList {
		if u := normalizeBaseURL(domain); u != "" {
			urls = append(urls, u)
		}
	}
	return uniqueStrings(urls)
}

func normalizeBaseURL(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	if !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
		value = "https://" + value
	}
	return strings.TrimRight(value, "/")
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
