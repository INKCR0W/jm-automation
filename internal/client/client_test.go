package client

import (
	"context"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/INKCR0W/jm-automation/internal/config"
	"github.com/INKCR0W/jm-automation/pkg/logger"
	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/bandwidth"
	"golang.org/x/net/proxy"
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

func TestClientConcurrentCookieAccessIsRaceFree(t *testing.T) {
	c := &Client{
		httpClient: &stubHTTPClient{},
		baseURL:    "https://example.test",
		cookieFile: t.TempDir() + "/session.json",
	}
	c.cookies = []*http.Cookie{{Name: "remember", Value: "initial"}}

	ctx := context.Background()
	start := make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 100; j++ {
				if _, err := c.doRequestRawOnce(ctx, http.MethodGet, c.baseURL, "/ok", "", nil); err != nil {
					t.Errorf("request failed: %v", err)
					return
				}
			}
		}()
	}

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			<-start
			for j := 0; j < 100; j++ {
				c.SetCookies([]*http.Cookie{{
					Name:  "remember",
					Value: strconv.Itoa(worker*100 + j),
				}})
			}
		}(i)
	}

	close(start)
	wg.Wait()
}

type stubHTTPClient struct{}

func (s *stubHTTPClient) GetCookies(_ *url.URL) []*http.Cookie    { return nil }
func (s *stubHTTPClient) SetCookies(_ *url.URL, _ []*http.Cookie) {}
func (s *stubHTTPClient) SetCookieJar(_ http.CookieJar)           {}
func (s *stubHTTPClient) GetCookieJar() http.CookieJar            { return nil }
func (s *stubHTTPClient) SetProxy(_ string) error                 { return nil }
func (s *stubHTTPClient) GetProxy() string                        { return "" }
func (s *stubHTTPClient) SetFollowRedirect(_ bool)                {}
func (s *stubHTTPClient) GetFollowRedirect() bool                 { return false }
func (s *stubHTTPClient) CloseIdleConnections()                   {}
func (s *stubHTTPClient) Do(_ *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
	}, nil
}
func (s *stubHTTPClient) Get(_ string) (*http.Response, error)  { return s.Do(nil) }
func (s *stubHTTPClient) Head(_ string) (*http.Response, error) { return s.Do(nil) }
func (s *stubHTTPClient) Post(_ string, _ string, _ io.Reader) (*http.Response, error) {
	return s.Do(nil)
}
func (s *stubHTTPClient) GetBandwidthTracker() bandwidth.BandwidthTracker       { return nil }
func (s *stubHTTPClient) GetDialer() proxy.ContextDialer                        { return nil }
func (s *stubHTTPClient) GetTLSDialer() tls_client.TLSDialerFunc                { return nil }
func (s *stubHTTPClient) AddPreRequestHook(_ tls_client.PreRequestHookFunc)     {}
func (s *stubHTTPClient) AddPostResponseHook(_ tls_client.PostResponseHookFunc) {}
func (s *stubHTTPClient) ResetPreHooks()                                        {}
func (s *stubHTTPClient) ResetPostHooks()                                       {}
