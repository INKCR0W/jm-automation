package client

import (
	"os"
	"testing"
	"time"

	"github.com/INKCR0W/jm-automation/internal/config"
	"github.com/INKCR0W/jm-automation/pkg/logger"
)

func TestLoadCookiesSupportsJWTOnlySession(t *testing.T) {
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
	defer func() { _ = os.Chdir(wd) }()

	c, err := New("https://example.test", time.Second, "jwt-only")
	if err != nil {
		t.Fatalf("new client failed: %v", err)
	}
	c.SetJWTToken("token-123")
	c.SetUserInfo("42", "tester")

	loaded, err := New("https://example.test", time.Second, "jwt-only")
	if err != nil {
		t.Fatalf("new client with persisted session failed: %v", err)
	}

	if got := loaded.GetJWTToken(); got != "token-123" {
		t.Fatalf("loaded jwt = %q, want token-123", got)
	}
	if got := loaded.GetUserID(); got != "42" {
		t.Fatalf("loaded user id = %q, want 42", got)
	}
	if !loaded.HasValidCookies() {
		t.Fatal("jwt-only session should be considered valid")
	}
}
