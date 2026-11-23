package client

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
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
		setTestResponseCookies(t, c, "https://api.example.test/root", []*http.Cookie{
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

	setTestResponseCookies(t, c, "https://api.example.test/root", []*http.Cookie{
		{Name: "ipcountry", Value: "TW"},
		{Name: "AVS", Value: "old"},
	})
	setTestResponseCookies(t, c, "https://api.example.test/root", []*http.Cookie{
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

	setTestResponseCookies(t, c, "https://api.example.test/root", []*http.Cookie{{Name: "AVS", Value: "from-server", Path: "/"}})
	setTestResponseCookies(t, c, "https://api.example.test/root", []*http.Cookie{{Name: "AVS", Value: "from-login", Path: ""}})

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

	setTestResponseCookies(t, c, "https://one.example.test", []*http.Cookie{
		{Name: "AVS", Value: "a", Path: "/"},
		{Name: "AVS", Value: "c", Path: "/sub"},
	})
	setTestResponseCookies(t, c, "https://two.example.test", []*http.Cookie{
		{Name: "AVS", Value: "b", Path: "/"},
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

			setTestResponseCookies(t, c, "https://xn--bcher-kva.example", []*http.Cookie{
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

func TestResponseCookieRejectsInvalidExplicitDomain(t *testing.T) {
	c := &Client{}
	setTestResponseCookies(t, c, "https://api.example.test", []*http.Cookie{{Name: "AVS", Value: "host-only"}})

	for _, domain := range []string{".", "bad domain", "other.example.test", ".test"} {
		setTestResponseCookies(t, c, "https://api.example.test", []*http.Cookie{
			{Name: "AVS", Value: "invalid", Domain: domain},
			{Name: "AVS", Domain: domain, MaxAge: -1},
		})
	}
	cookies := c.GetCookies()
	assertCookieNames(t, cookies, []string{"AVS"})
	if cookies[0].Domain != "" || cookies[0].Value != "host-only" {
		t.Fatalf("remaining AVS domain = %q, value = %q, want empty domain and host-only value", cookies[0].Domain, cookies[0].Value)
	}
}

func TestAppendCookiesHonorsDeletion(t *testing.T) {
	c := &Client{}
	setTestResponseCookies(t, c, "https://api.example.test/root", []*http.Cookie{
		{Name: "AVS", Value: "value"},
		{Name: "theme", Value: "dark"},
	})

	setTestResponseCookies(t, c, "https://api.example.test/root", []*http.Cookie{{Name: "AVS", Value: "", MaxAge: -1}})

	got := c.GetCookies()
	if len(got) != 1 || got[0].Name != "theme" {
		t.Fatalf("cookies = %+v, want only theme", got)
	}
}

func TestAppendCookiesKeepsPositiveMaxAgeWithPastExpires(t *testing.T) {
	c := &Client{}
	setTestResponseCookies(t, c, "https://api.example.test/root", []*http.Cookie{{
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

	for _, cookie := range cookies {
		normalizeResponseCookieExpiry(cookie, receivedAt)
	}

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
	normalizeResponseCookieExpiry(current, receivedAt)
	c := newSessionTestClient(t, "https://api.example.test")
	c.SetCookies([]*http.Cookie{current, old})
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
	bloated := make([]CookieData, 0, 600)
	for range 300 {
		bloated = append(bloated,
			CookieData{Name: "remember", Value: longValue},
			CookieData{Name: "remember_id", Value: longValue},
		)
	}
	writeSessionFile(t, c.cookieFile, bloated)

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

func setTestResponseCookies(t *testing.T, c *Client, rawURL string, cookies []*http.Cookie) {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	sessionCookieJar{c}.SetCookies(u, cookies)
}

func TestConcurrentResponseCookiesPersistLatestSnapshot(t *testing.T) {
	c := &Client{
		baseURL:    "https://example.test",
		cookieFile: filepath.Join(newSessionPersistenceTestDir(t), "session.json"),
	}
	u, err := url.Parse(c.BaseURL() + "/seed")
	if err != nil {
		t.Fatal(err)
	}
	jar := sessionCookieJar{c}
	start := make(chan struct{})
	var workers sync.WaitGroup
	for worker := range 4 {
		workers.Go(func() {
			<-start
			for n := range 6 {
				jar.SetCookies(u, []*http.Cookie{{Name: fmt.Sprintf("worker-%d", worker), Value: strconv.Itoa(n)}})
			}
		})
	}
	close(start)
	workers.Wait()

	reloaded := &Client{cookieFile: c.cookieFile, baseURL: c.BaseURL()}
	if err := reloaded.LoadCookies(); err != nil {
		t.Fatal(err)
	}
	cookies := reloaded.GetCookies()
	if len(cookies) != 4 {
		t.Fatalf("persisted Cookie count = %d, want 4", len(cookies))
	}
	for _, cookie := range cookies {
		if cookie.Value != "5" {
			t.Errorf("persisted %s value = %q, want final value", cookie.Name, cookie.Value)
		}
	}
	assertNoTemporarySessionFiles(t, c.cookieFile)
}

func newSessionPersistenceTestDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "jm-cookie-persistence-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Windows 偶尔会在所有会话写入结束后暂时报告目录非空
		const windowsDirectoryNotEmpty = syscall.Errno(145)
		const attempts = 10
		for attempt := range attempts {
			removeErr := os.RemoveAll(dir)
			if removeErr == nil {
				return
			}
			if runtime.GOOS != "windows" || !errors.Is(removeErr, windowsDirectoryNotEmpty) || attempt == attempts-1 {
				t.Errorf("clean up session directory: %v", removeErr)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
	return dir
}

func TestSessionWriteCleansUpAfterRenameFailure(t *testing.T) {
	target := filepath.Join(t.TempDir(), "session.json")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessionFileAtomically(target, []byte("session")); err == nil {
		t.Fatal("replacing a directory with the session file succeeded")
	}
	assertNoTemporarySessionFiles(t, target)
}

func assertNoTemporarySessionFiles(t *testing.T, sessionPath string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(sessionPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != filepath.Base(sessionPath) {
			t.Errorf("unexpected session directory entry %s", entry.Name())
		}
	}
}
