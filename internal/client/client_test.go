package client

import (
	"context"
	"errors"
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

func TestDoRequestOnceBuildsJSONRequest(t *testing.T) {
	stub := &stubHTTPClient{}
	c := &Client{
		httpClient: stub,
		baseURL:    "https://example.test",
		cookieFile: t.TempDir() + "/session.json",
	}
	c.applySession(nil, "", "", "jwt-123")

	resp, err := c.doRequestOnce(
		context.Background(),
		http.MethodPost,
		"https://example.test",
		"/api/login",
		map[string]string{"username": "tester"},
		map[string]string{"x-client": "jm-auto"},
	)
	if err != nil {
		t.Fatalf("doRequestOnce failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	req := stub.lastRequest(t)
	if req.method != http.MethodPost {
		t.Fatalf("method = %q, want %q", req.method, http.MethodPost)
	}
	if req.url != "https://example.test/api/login" {
		t.Fatalf("url = %q, want https://example.test/api/login", req.url)
	}
	if req.body != `{"username":"tester"}` {
		t.Fatalf("body = %q, want JSON username payload", req.body)
	}
	if got := req.header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type = %q, want application/json", got)
	}
	if got := req.header.Get("Authorization"); got != "Bearer jwt-123" {
		t.Fatalf("authorization = %q, want Bearer jwt-123", got)
	}
	if got := req.header.Get("x-client"); got != "jm-auto" {
		t.Fatalf("x-client = %q, want jm-auto", got)
	}
}

func TestDoRequestRawOncePreservesRawBodyAndCookies(t *testing.T) {
	stub := &stubHTTPClient{
		responseHeader: http.Header{
			"Set-Cookie": {"AVS=response-token; Path=/"},
		},
	}
	c := &Client{
		httpClient: stub,
		baseURL:    "https://example.test",
		cookieFile: t.TempDir() + "/session.json",
	}
	c.setCookies([]*http.Cookie{{Name: "remember", Value: "cookie-token"}})

	_, err := c.doRequestRawOnce(
		context.Background(),
		http.MethodPost,
		"https://example.test",
		"/api/checkin",
		"album_id=42&like=1",
		map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
	)
	if err != nil {
		t.Fatalf("doRequestRawOnce failed: %v", err)
	}

	req := stub.lastRequest(t)
	if req.body != "album_id=42&like=1" {
		t.Fatalf("body = %q, want raw form payload", req.body)
	}
	if got := req.header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
		t.Fatalf("content-type = %q, want application/x-www-form-urlencoded", got)
	}
	if got := req.header.Get("Cookie"); got != "remember=cookie-token" {
		t.Fatalf("cookie header = %q, want remember=cookie-token", got)
	}

	cookies := c.GetCookies()
	if len(cookies) != 2 {
		t.Fatalf("cookie count = %d, want 2", len(cookies))
	}
	if cookies[1].Name != "AVS" || cookies[1].Value != "response-token" {
		t.Fatalf("response cookie = %s=%s, want AVS=response-token", cookies[1].Name, cookies[1].Value)
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

type capturedRequest struct {
	method string
	url    string
	header http.Header
	body   string
}

type stubHTTPClient struct {
	mu             sync.Mutex
	requests       []capturedRequest
	responseHeader http.Header
}

func (s *stubHTTPClient) lastRequest(t *testing.T) capturedRequest {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.requests) == 0 {
		t.Fatal("no request captured")
	}
	return s.requests[len(s.requests)-1]
}

func (s *stubHTTPClient) GetCookies(_ *url.URL) []*http.Cookie    { return nil }
func (s *stubHTTPClient) SetCookies(_ *url.URL, _ []*http.Cookie) {}
func (s *stubHTTPClient) SetCookieJar(_ http.CookieJar)           {}
func (s *stubHTTPClient) GetCookieJar() http.CookieJar            { return nil }
func (s *stubHTTPClient) SetProxy(_ string) error                 { return nil }
func (s *stubHTTPClient) GetProxy() string                        { return "" }
func (s *stubHTTPClient) SetFollowRedirect(_ bool)                {}
func (s *stubHTTPClient) GetFollowRedirect() bool                 { return false }
func (s *stubHTTPClient) CloseIdleConnections()                   {}
func (s *stubHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, errors.New("request is nil")
	}

	var body string
	if req.Body != nil {
		bodyBytes, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		body = string(bodyBytes)
	}

	s.mu.Lock()
	s.requests = append(s.requests, capturedRequest{
		method: req.Method,
		url:    req.URL.String(),
		header: req.Header.Clone(),
		body:   body,
	})
	responseHeader := s.responseHeader.Clone()
	s.mu.Unlock()
	if responseHeader == nil {
		responseHeader = make(http.Header)
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     responseHeader,
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
