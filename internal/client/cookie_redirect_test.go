package client

import (
	"context"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	http "github.com/bogdanfinn/fhttp"
)

func TestRedirectSuppressionUsesEffectivePortAndScheme(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
		target string
		follow bool
	}{
		{name: "implicit HTTPS port", source: "https://api.example.test", target: "https://api.example.test:443", follow: true},
		{name: "implicit HTTP port", source: "http://api.example.test:80", target: "http://api.example.test", follow: true},
		{name: "different port", source: "http://api.example.test:8080", target: "http://api.example.test:8081"},
		{name: "downgrade on same port", source: "https://api.example.test:8443", target: "http://api.example.test:8443"},
		{name: "unrelated configured mirror", source: "https://api.example.test", target: "https://mirror.example.net"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{}
			c.SetBaseURLs([]string{tc.source, tc.target})
			c.SetLoginAVS(nil, "mirror")
			c.httpClient = &http.Client{
				Jar:           sessionCookieJar{c},
				CheckRedirect: checkRedirect,
				Transport: redirectTestTransport(func(req *http.Request) (*http.Response, error) {
					if req.Header.Get("Cookie") != "AVS=mirror" {
						t.Errorf("Cookie at %s = %q, want configured mirror cookie", req.URL, req.Header.Get("Cookie"))
					}
					switch req.URL.Path {
					case "/start":
						return redirectTestResponse(req, http.StatusFound, tc.target+"/target"), nil
					case "/target":
						if !tc.follow {
							t.Error("cross-origin redirect reached the target")
						}
						return redirectTestResponse(req, http.StatusOK, ""), nil
					default:
						return nil, fmt.Errorf("unexpected request to %s", req.URL)
					}
				}),
			}

			resp, err := c.Get(context.Background(), "/start", nil)
			if err != nil {
				t.Fatal(err)
			}
			want := http.StatusFound
			if tc.follow {
				want = http.StatusOK
			}
			if resp.StatusCode != want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, want)
			}
		})
	}
}

func TestTrustedSubdomainRedirectUsesTargetCookiesWithoutInitialCookie(t *testing.T) {
	const base = "https://example.test"
	const target = "https://api.example.test"
	c := &Client{baseURL: base}
	setTestResponseCookies(t, c, target+"/private/login", []*http.Cookie{
		{Name: "child", Value: "token", Secure: true},
	})

	c.httpClient = &http.Client{
		Jar:           sessionCookieJar{c},
		CheckRedirect: checkRedirect,
		Transport: redirectTestTransport(func(req *http.Request) (*http.Response, error) {
			switch req.URL.String() {
			case base + "/start":
				if cookie := req.Header.Get("Cookie"); cookie != "" {
					t.Errorf("initial Cookie = %q, want empty", cookie)
				}
				return redirectTestResponse(req, http.StatusFound, target+"/private/page"), nil
			case target + "/private/page":
				if cookie := req.Header.Get("Cookie"); cookie != "child=token" {
					t.Errorf("redirected Cookie = %q, want child=token", cookie)
				}
				return redirectTestResponse(req, http.StatusOK, ""), nil
			default:
				return nil, fmt.Errorf("unexpected request to %s", req.URL)
			}
		}),
	}
	resp, err := c.Get(context.Background(), "/start", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestRedirectCookiesUseEachResponseURLForDefaultPath(t *testing.T) {
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		switch r.URL.Path {
		case "/session/start":
			w.Header().Add("Set-Cookie", "hop=visited")
			stdhttp.Redirect(w, r, "/session/next", stdhttp.StatusFound)
		case "/session/next":
			if got := r.Header.Get("Cookie"); got != "hop=visited" {
				t.Errorf("second hop Cookie = %q, want hop=visited", got)
			}
			stdhttp.Redirect(w, r, "/account/login", stdhttp.StatusFound)
		case "/account/login":
			if got := r.Header.Get("Cookie"); got != "" {
				t.Errorf("other directory Cookie = %q, want empty", got)
			}
			w.Header().Add("Set-Cookie", "missing=path")
			w.Header().Add("Set-Cookie", "invalid=path; Path=wrong")
		case "/account/clear":
			w.Header().Add("Set-Cookie", "missing=; Max-Age=0")
			w.Header().Add("Set-Cookie", "invalid=; Max-Age=0; Path=wrong")
		case "/session/clear":
			w.Header().Add("Set-Cookie", "hop=; Max-Age=0")
		}
		_, _ = io.WriteString(w, r.Header.Get("Cookie"))
	}))
	t.Cleanup(server.Close)

	c, err := NewEphemeral(t.Context(), server.URL, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.cookieFile = t.TempDir() + "/session.json"
	request := func(path string) string {
		t.Helper()
		resp, requestErr := c.Get(context.Background(), path, nil)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		return string(resp.Body)
	}
	request("/session/start")
	for _, tc := range []struct{ path, want string }{
		{"/account/page", "missing=path; invalid=path"},
		{"/accounting/page", ""},
		{"/session/page", "hop=visited"},
	} {
		if got := request(tc.path); got != tc.want {
			t.Errorf("Cookie at %s = %q, want %q", tc.path, got, tc.want)
		}
	}
	request("/account/clear")
	request("/session/clear")

	reloaded := &Client{cookieFile: c.cookieFile, baseURL: c.BaseURL()}
	if err := reloaded.LoadCookies(); err != nil {
		t.Fatal(err)
	}
	assertCookieNames(t, reloaded.GetCookies(), nil)
}

func TestCookieScopesSurviveReloadAndMirrorChange(t *testing.T) {
	const primary = "https://one.example.test"
	const backup = "https://two.example.test"
	const third = "https://third.example.test"
	configured := []string{primary, backup}
	c := newSessionTestClient(t, primary)
	c.SetBaseURLs(configured)
	c.SetLoginAVS(nil, "mirror")
	setTestResponseCookies(t, c, primary+"/login", []*http.Cookie{
		{Name: "AVS", Value: "primary"},
		{Name: "host-only", Value: "primary"},
		{Name: "domain", Value: "shared", Domain: ".example.test"},
	})
	setTestResponseCookies(t, c, backup+"/nested/login", []*http.Cookie{
		{Name: "remember", Value: "backup"},
	})

	reloaded := &Client{cookieFile: c.cookieFile}
	reloaded.SetBaseURLs(configured)
	if err := reloaded.LoadCookies(); err != nil {
		t.Fatal(err)
	}
	jar := sessionCookieJar{reloaded}
	assertHeader := func(rawURL, want string) {
		t.Helper()
		u, err := url.Parse(rawURL)
		if err != nil {
			t.Fatal(err)
		}
		var pairs []string
		for _, cookie := range jar.Cookies(u) {
			pairs = append(pairs, cookie.Name+"="+cookie.Value)
		}
		if header := strings.Join(pairs, "; "); header != want {
			t.Errorf("Cookie at %s = %q, want %q", rawURL, header, want)
		}
	}
	assertHeader(primary+"/page", "AVS=primary; host-only=primary; domain=shared")
	assertHeader(backup+"/nested/page", "remember=backup; AVS=mirror; domain=shared")
	assertHeader(backup+"/public", "AVS=mirror; domain=shared")
	assertHeader("https://child.one.example.test/page", "domain=shared")
	assertHeader("https://two.example.test:8443/public", "domain=shared")

	reloaded.SetBaseURLs([]string{third})
	assertHeader(backup+"/nested/page", "remember=backup; domain=shared")
	assertHeader(third+"/public", "AVS=mirror; domain=shared")
}

type redirectTestTransport func(*http.Request) (*http.Response, error)

func (rt redirectTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return rt(req)
}

func redirectTestResponse(req *http.Request, status int, location string) *http.Response {
	res := &http.Response{Request: req, StatusCode: status, Header: make(http.Header), Body: http.NoBody}
	if location != "" {
		res.Header.Set("Location", location)
	}
	return res
}

func TestResponseCookieClearsMirrorAVS(t *testing.T) {
	for _, tc := range []struct {
		name       string
		wireCookie bool
	}{
		{name: "legacy fallback only"},
		{name: "legacy fallback and scoped cookie", wireCookie: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
				switch r.URL.Path {
				case "/set":
					w.Header().Add("Set-Cookie", "AVS=scoped; Path=/")
				case "/invalid":
					w.Header().Add("Set-Cookie", "AVS=; Domain= .; Path=/; Max-Age=0")
				case "/clear":
					w.Header().Add("Set-Cookie", "AVS=; Path=/; Max-Age=0")
				}
				_, _ = io.WriteString(w, r.Header.Get("Cookie"))
			}))
			t.Cleanup(server.Close)

			c, err := NewEphemeral(t.Context(), server.URL, 2*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			c.cookieFile = t.TempDir() + "/session.json"
			c.SetLoginAVS(nil, "legacy")
			request := func(path string) string {
				t.Helper()
				resp, requestErr := c.Get(context.Background(), path, nil)
				if requestErr != nil {
					t.Fatal(requestErr)
				}
				return string(resp.Body)
			}
			want := "AVS=legacy"
			if tc.wireCookie {
				request("/set")
				want = "AVS=scoped"
			}

			request("/invalid")
			if got := request("/page"); got != want {
				t.Fatalf("Cookie after invalid deletion = %q, want %q", got, want)
			}
			request("/clear")
			if got := request("/page"); got != "" {
				t.Fatalf("Cookie after valid deletion = %q, want empty", got)
			}

			reloaded := &Client{cookieFile: c.cookieFile, baseURL: server.URL}
			if err := reloaded.LoadCookies(); err != nil {
				t.Fatal(err)
			}
			assertCookieNames(t, reloaded.GetCookies(), nil)
		})
	}
}

func TestLoginAVSUpdatesOnlyTheFinalResponseHost(t *testing.T) {
	const base = "https://example.test"
	const child = "https://api.example.test"
	c := &Client{baseURL: base}
	setTestResponseCookies(t, c, base+"/seed", []*http.Cookie{{Name: "AVS", Value: "parent"}})
	setTestResponseCookies(t, c, child+"/seed", []*http.Cookie{{Name: "AVS", Value: "child"}})
	c.httpClient = &http.Client{
		Jar:           sessionCookieJar{c},
		CheckRedirect: checkRedirect,
		Transport: redirectTestTransport(func(req *http.Request) (*http.Response, error) {
			switch req.URL.String() {
			case base + "/login":
				return redirectTestResponse(req, http.StatusTemporaryRedirect, child+"/login"), nil
			case child + "/login":
				return redirectTestResponse(req, http.StatusOK, ""), nil
			case base + "/cookie", child + "/cookie":
				resp := redirectTestResponse(req, http.StatusOK, "")
				resp.Body = io.NopCloser(strings.NewReader(req.Header.Get("Cookie")))
				return resp, nil
			}
			return nil, fmt.Errorf("unexpected request to %s", req.URL)
		}),
	}
	ctx := context.Background()
	resp, err := c.PostForm(ctx, "/login", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.RequestURL; got == nil || got.String() != child+"/login" {
		t.Fatalf("response URL = %v, want %s", got, child+"/login")
	}
	c.SetLoginAVS(resp.RequestURL, "new")
	for baseURL, want := range map[string]string{base: "AVS=parent", child: "AVS=new"} {
		resp, err := c.doRequestRawOnce(ctx, http.MethodGet, baseURL, "/cookie", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := string(resp.Body); got != want {
			t.Errorf("Cookie at %s = %q, want %q", baseURL, got, want)
		}
	}
}

func TestCookieHeaderOrdersLongestPathFirst(t *testing.T) {
	const base = "https://example.test"
	c := &Client{baseURL: base}
	setTestResponseCookies(t, c, base+"/seed", []*http.Cookie{{Name: "AVS", Value: "root"}})
	setTestResponseCookies(t, c, base+"/account/seed", []*http.Cookie{
		{Name: "first", Value: "earlier"},
		{Name: "AVS", Value: "account"},
	})
	stub := &stubHTTPClient{}
	c.useStubHTTPClient(stub)
	if _, err := c.Get(context.Background(), "/account/view", nil); err != nil {
		t.Fatal(err)
	}
	if got := stub.lastRequest(t).header.Get("Cookie"); got != "first=earlier; AVS=account; AVS=root" {
		t.Fatalf("Cookie = %q, want longest paths first with ties in creation order", got)
	}
}

func TestLoginAVSOverridesRootCookiesOnlyAtItsOrigin(t *testing.T) {
	const origin = "https://api.example.test"
	const sibling = "https://sibling.example.test"
	const mirror = "https://mirror.example.net"
	c := newSessionTestClient(t, origin)
	c.SetBaseURLs([]string{origin, sibling, mirror})
	setTestResponseCookies(t, c, sibling+"/seed", []*http.Cookie{
		{Name: "AVS", Value: "parent", Domain: "example.test"},
	})
	setTestResponseCookies(t, c, origin+"/seed", []*http.Cookie{
		{Name: "AVS", Value: "host-domain", Domain: "api.example.test"},
	})
	setTestResponseCookies(t, c, origin+"/account/seed", []*http.Cookie{
		{Name: "AVS", Value: "account"},
	})
	loginURL, err := url.Parse(origin + "/auth/login")
	if err != nil {
		t.Fatal(err)
	}
	c.SetLoginAVS(loginURL, "body")

	reloaded := &Client{cookieFile: c.cookieFile}
	reloaded.SetBaseURLs([]string{origin, sibling, mirror})
	if err := reloaded.LoadCookies(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ url, header string }{
		{origin + "/home", "AVS=body"},
		{origin + ":443/home", "AVS=body"},
		{origin + "/account/home", "AVS=account; AVS=body"},
		{sibling + "/home", "AVS=parent"},
		{"https://child.api.example.test/home", "AVS=parent; AVS=host-domain"},
		{origin + ":8443/home", "AVS=parent; AVS=host-domain"},
		{"http://api.example.test/home", "AVS=parent; AVS=host-domain"},
		{mirror + "/home", "AVS=body"},
		{"https://unknown.example.net/home", ""},
	} {
		assertSessionCookieHeader(t, reloaded, tc.url, tc.header)
	}
}

func TestLoginAVSReturnsToWireCookiesAfterOriginUpdates(t *testing.T) {
	const origin = "https://api.example.test"
	const sibling = "https://sibling.example.test"
	const mirror = "https://mirror.example.net"
	c := newSessionTestClient(t, origin)
	c.SetBaseURLs([]string{origin, sibling, mirror})
	loginURL, err := url.Parse(origin + "/login")
	if err != nil {
		t.Fatal(err)
	}
	mirrorURL, err := url.Parse(mirror + "/login")
	if err != nil {
		t.Fatal(err)
	}
	c.SetLoginAVS(mirrorURL, "previous")
	c.SetLoginAVS(loginURL, "current")
	assertSessionCookieHeader(t, c, mirror+"/home", "AVS=current")

	setTestResponseCookies(t, c, sibling+"/seed", []*http.Cookie{
		{Name: "AVS", Value: "sibling", Domain: "example.test"},
	})
	assertSessionCookieHeader(t, c, origin+"/home", "AVS=current")
	assertSessionCookieHeader(t, c, mirror+"/home", "AVS=current")

	setTestResponseCookies(t, c, origin+"/rotate", []*http.Cookie{
		{Name: "AVS", Value: "server", Domain: "example.test"},
	})
	assertSessionCookieHeader(t, c, origin+"/home", "AVS=server")
	assertSessionCookieHeader(t, c, mirror+"/home", "")

	c.SetLoginAVS(loginURL, "login-again")
	setTestResponseCookies(t, c, origin+"/clear", []*http.Cookie{
		{Name: "AVS", MaxAge: -1, Domain: "example.test"},
	})
	reloaded := &Client{cookieFile: c.cookieFile}
	reloaded.SetBaseURLs([]string{origin, sibling, mirror})
	if err := reloaded.LoadCookies(); err != nil {
		t.Fatal(err)
	}
	assertSessionCookieHeader(t, reloaded, origin+"/home", "")
	assertSessionCookieHeader(t, reloaded, mirror+"/home", "")
}

func assertSessionCookieHeader(t *testing.T, c *Client, target, want string) {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	request := http.Request{Header: make(http.Header)}
	for _, cookie := range (sessionCookieJar{c}).Cookies(u) {
		request.AddCookie(cookie)
	}
	if got := request.Header.Get("Cookie"); got != want {
		t.Errorf("Cookie at %s = %q, want %q", target, got, want)
	}
}
