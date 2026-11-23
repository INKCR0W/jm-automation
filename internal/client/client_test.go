package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
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
)

func TestLoadCookiesSupportsJWTOnlySession(t *testing.T) {
	initClientTestLogger(t)

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

func TestLoadCookiesFiltersExpiredAndUnexpectedDomain(t *testing.T) {
	initClientTestLogger(t)

	c := newSessionTestClient(t, "https://api.example.test")
	writeSessionFile(t, c.cookieFile, SessionData{
		Cookies: []CookieData{
			{
				Name:    "valid",
				Value:   "1",
				Domain:  "api.example.test",
				Expires: time.Now().Add(time.Hour),
			},
			{
				Name:    "expired",
				Value:   "2",
				Domain:  "api.example.test",
				Expires: time.Now().Add(-time.Hour),
			},
			{
				Name:    "foreign",
				Value:   "3",
				Domain:  "evil.example.test",
				Expires: time.Now().Add(time.Hour),
			},
			{
				Name:    "host_only",
				Value:   "4",
				Expires: time.Now().Add(time.Hour),
			},
		},
	})

	if err := c.LoadCookies(); err != nil {
		t.Fatalf("load cookies failed: %v", err)
	}

	assertCookieNames(t, c.GetCookies(), []string{"valid", "host_only"})
}

func TestLoadCookiesDropsSecureCookiesForHTTPBaseURL(t *testing.T) {
	initClientTestLogger(t)

	c := newSessionTestClient(t, "http://api.example.test")
	writeSessionFile(t, c.cookieFile, SessionData{
		Cookies: []CookieData{
			{
				Name:    "secure",
				Value:   "1",
				Domain:  "api.example.test",
				Secure:  true,
				Expires: time.Now().Add(time.Hour),
			},
			{
				Name:    "plain",
				Value:   "2",
				Domain:  "api.example.test",
				Expires: time.Now().Add(time.Hour),
			},
		},
	})

	if err := c.LoadCookies(); err != nil {
		t.Fatalf("load cookies failed: %v", err)
	}

	assertCookieNames(t, c.GetCookies(), []string{"plain"})
}

func TestLoadCookiesSupportsLegacyCookieFormat(t *testing.T) {
	initClientTestLogger(t)

	c := newSessionTestClient(t, "https://api.example.test")
	writeSessionFile(t, c.cookieFile, []CookieData{
		{
			Name:    "remember",
			Value:   "legacy-token",
			Domain:  "api.example.test",
			Expires: time.Now().Add(time.Hour),
		},
	})

	if err := c.LoadCookies(); err != nil {
		t.Fatalf("load legacy cookies failed: %v", err)
	}

	cookies := c.GetCookies()
	assertCookieNames(t, cookies, []string{"remember"})
	if cookies[0].Value != "legacy-token" {
		t.Fatalf("legacy cookie value = %q, want legacy-token", cookies[0].Value)
	}
}

func TestSaveAndLoadCookiesPreservesSecurityAttributes(t *testing.T) {
	initClientTestLogger(t)

	c := newSessionTestClient(t, "https://api.example.test")
	c.SetCookies([]*http.Cookie{
		{
			Name:     "secure",
			Value:    "token",
			Domain:   "api.example.test",
			Path:     "/",
			Secure:   true,
			HttpOnly: true,
			Expires:  time.Now().Add(time.Hour),
		},
	})

	loaded := newSessionTestClient(t, "https://api.example.test")
	loaded.cookieFile = c.cookieFile
	if err := loaded.LoadCookies(); err != nil {
		t.Fatalf("load persisted cookies failed: %v", err)
	}

	cookies := loaded.GetCookies()
	assertCookieNames(t, cookies, []string{"secure"})
	if !cookies[0].Secure {
		t.Fatal("loaded cookie Secure = false, want true")
	}
	if !cookies[0].HttpOnly {
		t.Fatal("loaded cookie HttpOnly = false, want true")
	}
}

func TestDecryptResponseRejectsMalformedResponses(t *testing.T) {
	c := &Client{}
	ts := int64(1700000000000)

	tests := []struct {
		name    string
		resp    *Response
		wantErr string
	}{
		{
			name: "non json response",
			resp: &Response{
				StatusCode: http.StatusOK,
				Body:       []byte("plain text"),
				Headers: http.Header{
					"Content-Type": {"text/plain"},
				},
			},
			wantErr: "解析加密响应失败",
		},
		{
			name: "html response",
			resp: &Response{
				StatusCode: http.StatusOK,
				Body:       []byte("<html>blocked</html>"),
				Headers: http.Header{
					"Content-Type": {"text/html"},
				},
			},
			wantErr: "服务器返回 HTML",
		},
		{
			name: "api error response",
			resp: &Response{
				StatusCode: http.StatusOK,
				Body:       []byte(`{"code":-1,"errorMsg":"bad token"}`),
				Headers: http.Header{
					"Content-Type": {"application/json"},
				},
			},
			wantErr: "API 返回错误 (code=-1): bad token",
		},
		{
			name: "invalid base64 data",
			resp: &Response{
				StatusCode: http.StatusOK,
				Body:       []byte(`{"code":200,"data":"%%%not-base64%%%"}`),
				Headers: http.Header{
					"Content-Type": {"application/json"},
				},
			},
			wantErr: "base64 decode failed",
		},
		{
			name: "ciphertext is not block aligned",
			resp: &Response{
				StatusCode: http.StatusOK,
				Body:       []byte(`{"code":200,"data":"` + base64.StdEncoding.EncodeToString([]byte("short")) + `"}`),
				Headers: http.Header{
					"Content-Type": {"application/json"},
				},
			},
			wantErr: "ciphertext is not a multiple of the block size",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := c.DecryptResponse(tt.resp, ts)
			if err == nil {
				t.Fatal("DecryptResponse returned nil error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestDoRequestOnceBuildsJSONRequest(t *testing.T) {
	stub := &stubHTTPClient{}
	c := &Client{
		baseURL:    "https://example.test",
		cookieFile: t.TempDir() + "/session.json",
	}
	c.useStubHTTPClient(stub)
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
			"Set-Cookie": {"AVS=response-token; Path=/; Max-Age=3600; Expires=Fri, 06 Nov 1998 08:49:37 GMT"},
		},
	}
	c := &Client{
		baseURL:    "https://example.test",
		cookieFile: t.TempDir() + "/session.json",
	}
	c.useStubHTTPClient(stub)
	c.SetCookies([]*http.Cookie{{Name: "remember", Value: "cookie-token"}})

	requestStartedAt := time.Now()
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
	if cookies[1].MaxAge != 0 || cookies[1].Expires.Before(requestStartedAt.Add(time.Hour)) ||
		cookies[1].Expires.After(time.Now().Add(time.Hour)) {
		t.Fatalf("response cookie lifetime got MaxAge = %d, Expires = %v", cookies[1].MaxAge, cookies[1].Expires)
	}
}

func TestRequestsOnlySendMatchingCookies(t *testing.T) {
	cookies := []*http.Cookie{
		{Name: "AVS", Value: "legacy"},
		{Name: "secure", Value: "session", Secure: true},
		{Name: "domain", Value: "session", Domain: ".EXAMPLE.test"},
		{Name: "path", Value: "session", Path: "/private"},
		{Name: "encoded", Value: "session", Path: "/a%20b"},
		{Name: "ascii", Value: "session", Domain: "xn--bcher-kva.example"},
		{Name: "unicode", Value: "session", Domain: "bücher.example"},
		{Name: "invalid", Value: "session", Domain: "example.test."},
		{Name: "expired", Value: "session", MaxAge: 60},
	}
	normalizeResponseCookieExpiry(cookies[len(cookies)-1], time.Now().Add(-time.Hour))
	tests := []struct {
		name    string
		baseURL string
		path    string
		want    string
	}{
		{
			name:    "matching HTTPS subdomain and child path",
			baseURL: "https://api.example.test:8443",
			path:    "/private/page",
			want:    "path=session; AVS=legacy; secure=session; domain=session",
		},
		{
			name:    "same domain over HTTP",
			baseURL: "http://example.test",
			path:    "/private",
			want:    "path=session; AVS=legacy; domain=session",
		},
		{
			name:    "unrelated mirror and nonmatching path prefix",
			baseURL: "https://notexample.test",
			path:    "/privateer",
			want:    "AVS=legacy; secure=session",
		},
		{
			name:    "matching escaped path",
			baseURL: "https://api.example.test",
			path:    "/a%20b/favorite",
			want:    "encoded=session; AVS=legacy; secure=session; domain=session",
		},
		{
			name:    "escaped slash is not a path separator",
			baseURL: "https://api.example.test",
			path:    "/private%2Ffavorite",
			want:    "AVS=legacy; secure=session; domain=session",
		},
		{
			name:    "FQDN with root dot",
			baseURL: "https://api.example.test.:8443",
			path:    "/favorite",
			want:    "AVS=legacy; secure=session; domain=session",
		},
		{
			name:    "Unicode request host",
			baseURL: "https://bücher.example",
			path:    "/favorite",
			want:    "AVS=legacy; secure=session; ascii=session; unicode=session",
		},
		{
			name:    "ASCII request host",
			baseURL: "https://xn--bcher-kva.example",
			path:    "/favorite",
			want:    "AVS=legacy; secure=session; ascii=session; unicode=session",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &stubHTTPClient{}
			c := &Client{baseURL: tt.baseURL}
			c.useStubHTTPClient(stub)
			c.SetCookies(cookies)
			if _, err := c.Get(context.Background(), tt.path, nil); err != nil {
				t.Fatal(err)
			}
			req := stub.lastRequest(t)
			wantURL, err := url.Parse(tt.baseURL + tt.path)
			if err != nil {
				t.Fatal(err)
			}
			if req.url != wantURL.String() {
				t.Fatalf("URL = %q, want %q", req.url, wantURL)
			}
			if got := req.header.Get("Cookie"); got != tt.want {
				t.Fatalf("Cookie = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLoadCookiesMatchesCanonicalHost(t *testing.T) {
	c := newSessionTestClient(t, "https://bücher.example.:8443")
	writeSessionFile(t, c.cookieFile, SessionData{Cookies: []CookieData{
		{Name: "ascii", Domain: "xn--bcher-kva.example"},
		{Name: "legacy", Domain: "bücher.example"},
		{Name: "trailing-dot", Domain: "xn--bcher-kva.example."},
		{Name: "malformed", Domain: "bücher.example/invalid"},
		{Name: "host-only"},
	}})
	if err := c.LoadCookies(); err != nil {
		t.Fatal(err)
	}
	assertCookieNames(t, c.GetCookies(), []string{"ascii", "legacy", "host-only"})
}

func TestRedirectKeepsCookiesForCanonicalSameHost(t *testing.T) {
	initial, err := http.NewRequest(http.MethodGet, "https://bücher.example./private", nil)
	if err != nil {
		t.Fatal(err)
	}
	redirected, err := http.NewRequest(http.MethodGet, "https://xn--bcher-kva.example/public", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkRedirect(redirected, []*http.Request{initial}); err != nil {
		t.Fatal(err)
	}
}

func TestRedirectRechecksCookiePath(t *testing.T) {
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		switch r.URL.Path {
		case "/private/start":
			if got := r.Header.Get("Cookie"); got != "path=session; AVS=legacy" {
				t.Errorf("initial Cookie = %q", got)
			}
			stdhttp.Redirect(w, r, "/public", stdhttp.StatusFound)
		case "/public":
			_, _ = io.WriteString(w, r.Header.Get("Cookie"))
		default:
			t.Errorf("unexpected request to %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	c, err := NewEphemeral(server.URL, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.SetCookies([]*http.Cookie{
		{Name: "AVS", Value: "legacy"},
		{Name: "path", Value: "session", Path: "/private"},
	})
	resp, err := c.Get(context.Background(), "/private/start", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(resp.Body); got != "AVS=legacy" {
		t.Fatalf("redirected Cookie = %q, want %q", got, "AVS=legacy")
	}
}

func TestRedirectDoesNotRestoreCookiesOnUnrelatedHost(t *testing.T) {
	target := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if r.Header.Get("Cookie") != "" {
			t.Errorf("unrelated redirect received a Cookie on %s", r.URL.Path)
		}
		if r.URL.Path == "/relay" {
			stdhttp.Redirect(w, r, "/target", stdhttp.StatusFound)
		}
	}))
	t.Cleanup(target.Close)

	initial := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if got := r.Header.Get("Cookie"); got != "AVS=legacy" {
			t.Errorf("initial Cookie = %q, want AVS=legacy", got)
		}
		stdhttp.Redirect(w, r, target.URL+"/relay", stdhttp.StatusFound)
	}))
	t.Cleanup(initial.Close)

	baseURL := strings.Replace(initial.URL, "127.0.0.1", "localhost", 1)
	c, err := NewEphemeral(baseURL, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.SetCookies([]*http.Cookie{{Name: "AVS", Value: "legacy"}})
	if _, err := c.Get(context.Background(), "/start", nil); err != nil {
		t.Fatal(err)
	}
}

func TestDoWithBaseURLFallbackRetriesHTMLAndPromotesWorkingBaseURL(t *testing.T) {
	c := &Client{
		baseURL:    "https://html.test",
		baseURLs:   []string{"https://html.test", "https://json.test"},
		cookieFile: t.TempDir() + "/session.json",
	}

	calls := make([]string, 0, 2)
	resp, err := c.doWithBaseURLFallback(http.MethodGet, func(baseURL string) (*Response, error) {
		calls = append(calls, baseURL)
		if baseURL == "https://html.test" {
			return &Response{
				StatusCode: http.StatusOK,
				Body:       []byte("<html>blocked</html>"),
				Headers: http.Header{
					"Content-Type": {"text/html; charset=utf-8"},
				},
			}, nil
		}

		return &Response{
			StatusCode: http.StatusOK,
			Body:       []byte(`{"ok":true}`),
			Headers: http.Header{
				"Content-Type": {"application/json"},
			},
		}, nil
	})
	if err != nil {
		t.Fatalf("fallback request failed: %v", err)
	}

	if got := strings.Join(calls, ","); got != "https://html.test,https://json.test" {
		t.Fatalf("called base URLs = %q, want html then json", got)
	}
	if string(resp.Body) != `{"ok":true}` {
		t.Fatalf("response body = %q, want json response from fallback base URL", string(resp.Body))
	}
	if got := c.BaseURL(); got != "https://json.test" {
		t.Fatalf("baseURL = %q, want promoted working base URL", got)
	}
}

func TestDoWithBaseURLFallbackReturnsLastHTMLResponse(t *testing.T) {
	c := &Client{
		baseURL:    "https://one.test",
		baseURLs:   []string{"https://one.test", "https://two.test"},
		cookieFile: t.TempDir() + "/session.json",
	}

	resp, err := c.doWithBaseURLFallback(http.MethodGet, func(baseURL string) (*Response, error) {
		return &Response{
			StatusCode: http.StatusOK,
			Body:       []byte("<html>" + baseURL + "</html>"),
			Headers: http.Header{
				"Content-Type": {"text/html"},
			},
		}, nil
	})
	if err != nil {
		t.Fatalf("all-html fallback returned error: %v", err)
	}

	if string(resp.Body) != "<html>https://two.test</html>" {
		t.Fatalf("response body = %q, want last HTML response", string(resp.Body))
	}
	if got := c.BaseURL(); got != "https://one.test" {
		t.Fatalf("baseURL = %q, want original base URL when every candidate returns HTML", got)
	}
}

func TestClientConcurrentCookieAccessIsRaceFree(t *testing.T) {
	c := &Client{baseURL: "https://example.test"}
	c.useStubHTTPClient(&stubHTTPClient{})
	c.SetCookies([]*http.Cookie{{Name: "remember", Value: "initial"}})

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

func (c *Client) useStubHTTPClient(stub *stubHTTPClient) {
	c.httpClient = &http.Client{
		Transport:     stub,
		Jar:           sessionCookieJar{c},
		CheckRedirect: checkRedirect,
	}
}

func (s *stubHTTPClient) RoundTrip(req *http.Request) (*http.Response, error) {
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

func initClientTestLogger(t *testing.T) {
	t.Helper()

	if err := logger.Init(config.LogConfig{Level: "error"}); err != nil {
		t.Fatalf("init logger failed: %v", err)
	}
	t.Cleanup(logger.Sync)
}

func newSessionTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()

	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		cookieFile: t.TempDir() + "/session.json",
	}
}

func writeSessionFile(t *testing.T, path string, data interface{}) {
	t.Helper()

	bytes, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal session data failed: %v", err)
	}
	if err := os.WriteFile(path, bytes, 0600); err != nil {
		t.Fatalf("write session file failed: %v", err)
	}
}

func assertCookieNames(t *testing.T, cookies []*http.Cookie, want []string) {
	t.Helper()

	if len(cookies) != len(want) {
		t.Fatalf("cookie count = %d, want %d (%v)", len(cookies), len(want), want)
	}
	for i, name := range want {
		if cookies[i].Name != name {
			t.Fatalf("cookie[%d].Name = %q, want %q", i, cookies[i].Name, name)
		}
	}
}
