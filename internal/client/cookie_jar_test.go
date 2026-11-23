package client

import (
	"math"
	"strings"
	"testing"
	"time"

	http "github.com/bogdanfinn/fhttp"
)

func cookieHeaderSize(cookies []*http.Cookie) int {
	total := 0
	for _, cookie := range cookies {
		total += len(cookie.Name) + len(cookie.Value) + 3
	}
	return total
}

// 回归：旧实现无条件追加，跑久了 Cookie 头会撑爆，服务端直接回 400 HTML
func TestAppendCookiesDoesNotGrowOnRepeatedResponses(t *testing.T) {
	c := &Client{}

	longValue := strings.Repeat("a", 218)
	for range 500 {
		c.appendCookies([]*http.Cookie{
			{Name: "remember", Value: longValue},
			{Name: "remember_id", Value: longValue},
			{Name: "AVS", Value: "avs-value-0123456789012345"},
		})
	}

	got := c.GetCookies()
	if len(got) != 3 {
		t.Fatalf("cookie count = %d, want 3", len(got))
	}
	if size := cookieHeaderSize(got); size > maxCookieHeaderBytes {
		t.Fatalf("cookie header size = %d, want <= %d", size, maxCookieHeaderBytes)
	}
}

func TestAppendCookiesOverwritesValueAndKeepsOrder(t *testing.T) {
	c := &Client{}

	c.appendCookies([]*http.Cookie{
		{Name: "ipcountry", Value: "TW"},
		{Name: "AVS", Value: "old"},
	})
	c.appendCookies([]*http.Cookie{
		{Name: "AVS", Value: "new"},
		{Name: "theme", Value: "dark"},
	})

	got := c.GetCookies()
	want := []struct{ name, value string }{
		{"ipcountry", "TW"},
		{"AVS", "new"},
		{"theme", "dark"},
	}
	if len(got) != len(want) {
		t.Fatalf("cookie count = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Name != w.name || got[i].Value != w.value {
			t.Fatalf("cookie[%d] = %s=%s, want %s=%s", i, got[i].Name, got[i].Value, w.name, w.value)
		}
	}
}

// 回归：服务端下发的 AVS 带 path="/"，登录响应里自己造的不带
// 不归一化两个会并存，上游按最后一个取值，读到旧的，登录态直接失效
func TestAppendCookiesTreatsEmptyPathAsRoot(t *testing.T) {
	c := &Client{}

	c.appendCookies([]*http.Cookie{{Name: "AVS", Value: "from-server", Path: "/"}})
	c.appendCookies([]*http.Cookie{{Name: "AVS", Value: "from-login", Path: ""}})

	got := c.GetCookies()
	if len(got) != 1 {
		t.Fatalf("cookie count = %d, want 1 (got %+v)", len(got), got)
	}
	if got[0].Value != "from-login" {
		t.Fatalf("AVS = %q, want the newest value", got[0].Value)
	}
}

func TestAppendCookiesKeepsDistinctDomainAndPath(t *testing.T) {
	c := &Client{}

	c.appendCookies([]*http.Cookie{
		{Name: "AVS", Value: "a", Domain: "www.cdnhjk.net", Path: "/"},
		{Name: "AVS", Value: "b", Domain: "www.cdngwc.cc", Path: "/"},
		{Name: "AVS", Value: "c", Domain: "www.cdnhjk.net", Path: "/sub"},
	})

	if got := c.GetCookies(); len(got) != 3 {
		t.Fatalf("cookie count = %d, want 3", len(got))
	}
}

func TestAppendCookiesTreatsIDNDomainFormsAsOneCookie(t *testing.T) {
	for _, tt := range []struct {
		name   string
		maxAge int
		want   int
	}{
		{name: "replace", want: 1},
		{name: "delete", maxAge: -1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := newSessionTestClient(t, "https://xn--bcher-kva.example")
			writeSessionFile(t, c.cookieFile, []CookieData{
				{Name: "AVS", Value: "old", Domain: "bücher.example"},
			})
			if err := c.LoadCookies(); err != nil {
				t.Fatal(err)
			}
			assertCookieNames(t, c.GetCookies(), []string{"AVS"})

			c.appendCookies([]*http.Cookie{
				{Name: "AVS", Value: "updated", Domain: ".xn--bcher-kva.example", Path: "/", MaxAge: tt.maxAge},
			})
			cookies := c.GetCookies()
			if len(cookies) != tt.want {
				t.Fatalf("cookie count = %d, want %d", len(cookies), tt.want)
			}
			if tt.want == 1 && cookies[0].Value != "updated" {
				t.Fatalf("AVS value = %q, want updated", cookies[0].Value)
			}
		})
	}
}

func TestAppendCookiesKeepsInvalidDomainSeparateFromEmptyDomain(t *testing.T) {
	c := &Client{}
	c.appendCookies([]*http.Cookie{
		{Name: "AVS", Value: "legacy"},
		{Name: "AVS", Value: "invalid", Domain: "bad domain"},
	})
	if got := c.GetCookies(); len(got) != 2 {
		t.Fatalf("cookie count = %d, want 2", len(got))
	}

	c.appendCookies([]*http.Cookie{
		{Name: "AVS", Domain: ".BAD DOMAIN", MaxAge: -1},
	})
	cookies := c.GetCookies()
	assertCookieNames(t, cookies, []string{"AVS"})
	if cookies[0].Domain != "" || cookies[0].Value != "legacy" {
		t.Fatalf("remaining AVS domain = %q, value = %q, want empty domain and legacy value", cookies[0].Domain, cookies[0].Value)
	}
}

func TestAppendCookiesHonorsDeletion(t *testing.T) {
	c := &Client{}
	c.appendCookies([]*http.Cookie{
		{Name: "AVS", Value: "value"},
		{Name: "theme", Value: "dark"},
	})

	c.appendCookies([]*http.Cookie{{Name: "AVS", Value: "", MaxAge: -1}})

	got := c.GetCookies()
	if len(got) != 1 || got[0].Name != "theme" {
		t.Fatalf("cookies = %+v, want only theme", got)
	}
}

func TestAppendCookiesKeepsPositiveMaxAgeWithPastExpires(t *testing.T) {
	c := &Client{}
	c.appendCookies([]*http.Cookie{{
		Name:    "AVS",
		Value:   "value",
		MaxAge:  3600,
		Expires: time.Now().Add(-time.Hour),
	}})

	if got := c.GetCookies(); len(got) != 1 {
		t.Fatalf("cookie count = %d, want 1", len(got))
	}
}

func TestResponseCookieLifetimesUseReceiptTime(t *testing.T) {
	receivedAt := time.Date(2026, time.November, 23, 0, 0, 0, 0, time.UTC)
	past := receivedAt.Add(-time.Hour)
	cookies := []*http.Cookie{
		{Name: "max-age", MaxAge: 60},
		{Name: "both", MaxAge: 60, Expires: past},
		{Name: "absolute", Expires: past},
		{Name: "delete", MaxAge: -1},
		{Name: "long", MaxAge: math.MaxInt},
	}

	normalizeResponseCookieExpirations(cookies, receivedAt)

	for _, cookie := range cookies[:2] {
		if cookie.MaxAge != 0 || !cookie.Expires.Equal(receivedAt.Add(time.Minute)) {
			t.Fatalf("%s got MaxAge = %d, Expires = %v, want expiry one minute after receipt", cookie.Name, cookie.MaxAge, cookie.Expires)
		}
	}
	if !cookies[2].Expires.Equal(past) || cookies[3].MaxAge != -1 || !cookies[3].Expires.IsZero() {
		t.Fatal("absolute and deletion cookie lifetimes changed")
	}
	if cookies[4].MaxAge != 0 || !cookies[4].Expires.After(receivedAt.Add(24*time.Hour)) {
		t.Fatalf("large MaxAge got MaxAge = %d, Expires = %v", cookies[4].MaxAge, cookies[4].Expires)
	}
}

func TestCookieExpirySurvivesCacheReload(t *testing.T) {
	receivedAt := time.Now().Truncate(time.Second).Add(-time.Hour)
	current := &http.Cookie{Name: "AVS", Value: "current", MaxAge: 7200}
	old := &http.Cookie{Name: "remember", Value: "legacy", MaxAge: 1}
	normalizeResponseCookieExpirations([]*http.Cookie{current}, receivedAt)
	c := newSessionTestClient(t, "https://api.example.test")
	c.setCookies([]*http.Cookie{current, old})
	if err := c.SaveCookies(); err != nil {
		t.Fatal(err)
	}

	reloaded := &Client{cookieFile: c.cookieFile, baseURL: c.baseURL}
	if err := reloaded.LoadCookies(); err != nil {
		t.Fatal(err)
	}
	cookies := reloaded.GetCookies()
	assertCookieNames(t, cookies, []string{"AVS", "remember"})
	if cookies[0].MaxAge != 0 || !cookies[0].Expires.Equal(receivedAt.Add(2*time.Hour)) {
		t.Fatalf("current cache got MaxAge = %d, Expires = %v", cookies[0].MaxAge, cookies[0].Expires)
	}
	if cookies[1].MaxAge != 1 || !cookies[1].Expires.IsZero() {
		t.Fatalf("legacy cache got MaxAge = %d, Expires = %v", cookies[1].MaxAge, cookies[1].Expires)
	}

	writeSessionFile(t, c.cookieFile, []CookieData{{Name: "remember", Value: "legacy", MaxAge: 1}})
	reloaded = &Client{cookieFile: c.cookieFile, baseURL: c.baseURL}
	if err := reloaded.LoadCookies(); err != nil {
		t.Fatal(err)
	}
	cookies = reloaded.GetCookies()
	assertCookieNames(t, cookies, []string{"remember"})
	if cookies[0].MaxAge != 1 || !cookies[0].Expires.IsZero() {
		t.Fatalf("old cache format got MaxAge = %d, Expires = %v", cookies[0].MaxAge, cookies[0].Expires)
	}
}

func TestLoadCookiesCollapsesDuplicatePersistedCookies(t *testing.T) {
	dir := t.TempDir()
	c := &Client{cookieFile: dir + "/session.json"}

	longValue := strings.Repeat("a", 218)
	bloated := make([]*http.Cookie, 0, 600)
	for range 300 {
		bloated = append(bloated,
			&http.Cookie{Name: "remember", Value: longValue},
			&http.Cookie{Name: "remember_id", Value: longValue},
		)
	}
	// 绕开去重，直接造一个旧版本那种臃肿会话文件
	c.cookies = cloneCookies(bloated)
	if err := c.SaveCookies(); err != nil {
		t.Fatalf("SaveCookies returned error: %v", err)
	}

	reloaded := &Client{cookieFile: c.cookieFile}
	if err := reloaded.LoadCookies(); err != nil {
		t.Fatalf("LoadCookies returned error: %v", err)
	}

	got := reloaded.GetCookies()
	if len(got) != 2 {
		t.Fatalf("cookie count = %d, want 2", len(got))
	}
	if size := cookieHeaderSize(got); size > maxCookieHeaderBytes {
		t.Fatalf("cookie header size = %d, want <= %d", size, maxCookieHeaderBytes)
	}
}

func TestCappedCookiesTruncatesOversizedJar(t *testing.T) {
	longValue := strings.Repeat("a", 1000)
	cookies := make([]*http.Cookie, 0, 20)
	for range 20 {
		cookies = append(cookies, &http.Cookie{Name: "big", Value: longValue})
	}

	got := cappedCookies(cookies)
	if size := cookieHeaderSize(got); size > maxCookieHeaderBytes {
		t.Fatalf("cookie header size = %d, want <= %d", size, maxCookieHeaderBytes)
	}
	if len(got) == 0 || len(got) == len(cookies) {
		t.Fatalf("kept %d cookies, want a non-empty truncation", len(got))
	}
}

func TestCappedCookiesNeverDropsAuthCookies(t *testing.T) {
	longValue := strings.Repeat("a", 900)
	cookies := make([]*http.Cookie, 0, 12)
	for range 10 {
		cookies = append(cookies, &http.Cookie{Name: "junk", Value: longValue, Domain: "stale.example.net"})
	}
	// 排在最后，正好是朴素截断最先丢掉的位置
	cookies = append(cookies,
		&http.Cookie{Name: "remember", Value: strings.Repeat("r", 218)},
		&http.Cookie{Name: "AVS", Value: "the-live-session-value"},
	)

	got := cappedCookies(cookies)

	found := map[string]bool{}
	for _, cookie := range got {
		found[cookie.Name] = true
	}
	if !found["AVS"] || !found["remember"] {
		t.Fatalf("auth cookies dropped, kept = %+v", got)
	}
	if len(got) == len(cookies) {
		t.Fatal("nothing was dropped, expected the junk cookies to be trimmed")
	}
}

// 清空 jar 等于不带登录态发请求
func TestCappedCookiesKeepsSingleOversizedAuthCookie(t *testing.T) {
	cookies := []*http.Cookie{{Name: "AVS", Value: strings.Repeat("a", maxCookieHeaderBytes+100)}}

	got := cappedCookies(cookies)
	if len(got) != 1 || got[0].Name != "AVS" {
		t.Fatalf("kept = %+v, want the AVS cookie", got)
	}
}

func TestCappedCookiesKeepsNormalJarIntact(t *testing.T) {
	cookies := []*http.Cookie{
		{Name: "AVS", Value: strings.Repeat("a", 26)},
		{Name: "remember", Value: strings.Repeat("b", 218)},
		{Name: "remember_id", Value: strings.Repeat("c", 218)},
		{Name: "ipm5", Value: strings.Repeat("d", 32)},
	}

	if got := cappedCookies(cookies); len(got) != len(cookies) {
		t.Fatalf("kept %d cookies, want %d", len(got), len(cookies))
	}
}
