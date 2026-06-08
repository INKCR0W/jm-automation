package scheduler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/INKCR0W/jm-automation/internal/api"
	"github.com/INKCR0W/jm-automation/internal/client"
	"github.com/INKCR0W/jm-automation/internal/config"
	"github.com/INKCR0W/jm-automation/pkg/logger"
)

func TestGetOrCreateClientLoadsCookiesAfterBaseURLsAreSet(t *testing.T) {
	if err := logger.Init(config.LogConfig{Level: "error"}); err != nil {
		t.Fatalf("init logger failed: %v", err)
	}
	t.Cleanup(logger.Sync)

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}
	tmpDir := t.TempDir()
	if chdirErr := os.Chdir(tmpDir); chdirErr != nil {
		t.Fatalf("chdir temp dir failed: %v", chdirErr)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	cookieDir := filepath.Join(tmpDir, "data", "cookies")
	if mkdirErr := os.MkdirAll(cookieDir, 0755); mkdirErr != nil {
		t.Fatalf("create cookie dir failed: %v", mkdirErr)
	}
	session := client.SessionData{
		Cookies: []client.CookieData{
			{
				Name:    "remember",
				Value:   "fallback-token",
				Domain:  "fallback.example.test",
				Expires: time.Now().Add(time.Hour),
			},
		},
	}
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("marshal session failed: %v", err)
	}
	if writeErr := os.WriteFile(filepath.Join(cookieDir, "tester_session.json"), data, 0600); writeErr != nil {
		t.Fatalf("write session file failed: %v", writeErr)
	}

	s := &Scheduler{
		config: &config.Config{
			Server: config.ServerConfig{
				BaseURL: "https://primary.example.test",
				Timeout: 1,
			},
		},
		clientMap: make(map[string]*client.Client),
		baseURLs: []string{
			"https://primary.example.test",
			"https://fallback.example.test",
		},
	}

	c, err := s.getOrCreateClient("tester")
	if err != nil {
		t.Fatalf("get client failed: %v", err)
	}

	cookies := c.GetCookies()
	if len(cookies) != 1 {
		t.Fatalf("cookie count = %d, want 1", len(cookies))
	}
	if cookies[0].Name != "remember" || cookies[0].Value != "fallback-token" {
		t.Fatalf("cookie = %s=%s, want remember=fallback-token", cookies[0].Name, cookies[0].Value)
	}
}

func TestUniqueBaseURLsNormalizesAndDeduplicates(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   []string
	}{
		{
			name: "filters empty values",
			values: []string{
				"",
				"   ",
				"https://api.example.test",
			},
			want: []string{"https://api.example.test"},
		},
		{
			name: "deduplicates after trimming whitespace and trailing slash",
			values: []string{
				" https://api.example.test/ ",
				"https://api.example.test",
				"https://fallback.example.test///",
			},
			want: []string{
				"https://api.example.test",
				"https://fallback.example.test",
			},
		},
		{
			name: "falls back to default base url when all values are empty",
			values: []string{
				"",
				"   ",
				"////",
			},
			want: []string{api.DefaultBaseURL},
		},
		{
			name: "keeps invalid values for backward compatibility",
			values: []string{
				" not-a-url/ ",
			},
			want: []string{"not-a-url"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := uniqueBaseURLs(tt.values)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("uniqueBaseURLs() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
