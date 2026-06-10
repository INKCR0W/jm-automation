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

	"github.com/INKCR0W/jm-automation/pkg/crypto"
	"github.com/INKCR0W/jm-automation/pkg/logger"
)

type hostConfig struct {
	Server []string `json:"Server"`
}

func ResolveDynamicBaseURLs(ctx context.Context) []string {
	raw, err := fetchHostConfig(ctx)
	if err != nil {
		logger.Warn("获取远程域名配置失败，使用内置域名", "error", err)
		return fallbackBaseURLs()
	}

	compact := regexp.MustCompile(`[^A-Za-z0-9+/=]`).ReplaceAllString(raw, "")
	key := crypto.MD5Hex(HostConfigSecret)
	plaintext, err := crypto.DecodeBase64ECBPKCS7(compact, key)
	if err != nil {
		logger.Warn("解密远程域名配置失败，使用内置域名", "error", err)
		return fallbackBaseURLs()
	}

	var cfg hostConfig
	if err := json.Unmarshal([]byte(plaintext), &cfg); err != nil {
		logger.Warn("解析远程域名配置失败，使用内置域名", "error", err)
		return fallbackBaseURLs()
	}

	urls := make([]string, 0, len(cfg.Server)+len(DomainAPIList))
	for _, domain := range cfg.Server {
		if u := normalizeBaseURL(domain); u != "" {
			urls = append(urls, u)
		}
	}
	urls = append(urls, fallbackBaseURLs()...)

	return uniqueStrings(urls)
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
