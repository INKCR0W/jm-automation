package client

import (
	"cmp"
	"fmt"
	"math"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/INKCR0W/jm-automation/pkg/logger"
	http "github.com/bogdanfinn/fhttp"
	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

type sessionCookie struct {
	http.Cookie
	hostOnly    string
	loginOrigin cookieOrigin
}

type sessionCookieJar struct {
	client *Client
}

func (jar sessionCookieJar) Cookies(u *url.URL) []*http.Cookie {
	origin := cookieOriginOf(u)
	if origin.host == "" {
		return nil
	}

	c := jar.client
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()

	path := normalizeCookiePath(u.EscapedPath())
	now := time.Now()
	matching := make([]*http.Cookie, 0, len(c.cookies))
	var mirrorAVS *http.Cookie
	var hasOriginAVS, hasScopedAVS bool
	for _, entry := range c.cookies {
		if isDeletionCookie(&entry.Cookie, now) || entry.Secure && origin.scheme != "https" {
			continue
		}
		if path != entry.Path && !strings.HasPrefix(path, strings.TrimSuffix(entry.Path, "/")+"/") {
			continue
		}
		cookie := &http.Cookie{Name: entry.Name, Value: entry.Value, Path: entry.Path, Quoted: entry.Quoted}
		switch {
		case entry.isMirrorAVS():
			hasOriginAVS = entry.loginOrigin == origin
			if !hasOriginAVS && !c.isCookieMirrorLocked(origin) {
				continue
			}
			mirrorAVS = cookie
		case entry.hostOnly == origin.host || cookieDomainMatches(origin.host, entry.Domain):
			hasScopedAVS = hasScopedAVS || entry.Name == "AVS"
		default:
			continue
		}
		matching = append(matching, cookie)
	}
	if mirrorAVS != nil {
		matching = slices.DeleteFunc(matching, func(cookie *http.Cookie) bool {
			// 最终登录 origin 优先使用登录体的根 AVS，其他镜像只用它补缺
			if hasOriginAVS {
				return cookie != mirrorAVS && isRootAVS(cookie)
			}
			return cookie == mirrorAVS && hasScopedAVS
		})
	}
	slices.SortStableFunc(matching, func(a, b *http.Cookie) int {
		return cmp.Compare(len(b.Path), len(a.Path))
	})
	return cappedCookies(matching)
}

func (jar sessionCookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	origin := cookieOriginOf(u)
	if origin.host == "" {
		return
	}

	now := time.Now()
	defaultPath := defaultCookiePath(u.EscapedPath())
	entries := make([]sessionCookie, 0, len(cookies))
	var setsRootAVS, revokesMirrorAVS bool
	for _, cookie := range cookies {
		entry, ok := newSessionCookie(cookie)
		if !ok {
			continue
		}
		if entry.Domain != "" && !cookieDomainMatches(origin.host, entry.Domain) {
			continue
		}
		// 与 fhttp 的 Jar 一样，只有来源主机正好等于公共后缀时才接受该域，并按 host-only 存储
		if entry.Domain != "" && isPublicCookieSuffix(entry.Domain) && entry.Domain != origin.host {
			continue
		}
		if entry.Domain == "" || isPublicCookieSuffix(entry.Domain) || net.ParseIP(origin.host) != nil {
			entry.Domain = ""
			entry.hostOnly = origin.host
		}
		if !strings.HasPrefix(cookie.Path, "/") {
			entry.Path = defaultPath
		}
		normalizeResponseCookieExpiry(&entry.Cookie, now)
		entries = append(entries, entry)
		if isRootAVS(&entry.Cookie) {
			setsRootAVS = true
			revokesMirrorAVS = revokesMirrorAVS || cookie.Domain == "" && isDeletionCookie(&entry.Cookie, now)
		}
	}
	if len(entries) == 0 {
		return
	}

	c := jar.client
	c.sessionMu.Lock()
	// 登录来源回写根 AVS 后，之前的登录体值不再作为该来源或镜像的登录态
	supersedesLogin := setsRootAVS && slices.ContainsFunc(c.cookies, func(entry sessionCookie) bool {
		return entry.loginOrigin == origin
	})
	if supersedesLogin || revokesMirrorAVS && c.isCookieMirrorLocked(origin) {
		entries = append(entries, mirrorAVSCookie(""))
	}
	c.cookies = mergeCookies(c.cookies, entries, now)
	c.sessionMu.Unlock()
	c.saveCookies()
}

type cookieOrigin struct {
	scheme string
	host   string
	port   string
}

func cookieOriginOf(u *url.URL) cookieOrigin {
	if u == nil {
		return cookieOrigin{}
	}
	var port string
	switch u.Scheme {
	case "https":
		port = "443"
	case "http":
		port = "80"
	default:
		return cookieOrigin{}
	}
	if explicit := u.Port(); explicit != "" {
		number, err := strconv.ParseUint(explicit, 10, 16)
		if err != nil {
			return cookieOrigin{}
		}
		port = strconv.FormatUint(number, 10)
	}
	return cookieOrigin{scheme: u.Scheme, host: canonicalCookieHost(u.Hostname()), port: port}
}

func (o cookieOrigin) cacheKey() string {
	if o.host == "" {
		return ""
	}
	u := url.URL{Scheme: o.scheme, Host: net.JoinHostPort(o.host, o.port)}
	return u.String()
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}

	initial, target := cookieOriginOf(via[0].URL), cookieOriginOf(req.URL)
	// Jar 会在回调完成后为目标重新选择 Cookie，因此越界跳转直接返回原 3xx
	if initial.scheme != target.scheme || initial.port != target.port || !cookieDomainMatches(target.host, initial.host) {
		return http.ErrUseLastResponse
	}
	return nil
}

func canonicalCookieHost(host string) string {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	ascii, err := idna.ToASCII(host)
	if err != nil {
		return ""
	}
	return ascii
}

func cookieDomainMatches(host, domain string) bool {
	return domain != "" && (host == domain || strings.HasSuffix(host, "."+domain) && net.ParseIP(host) == nil)
}

func normalizeCookieDomain(domain string) (string, bool) {
	if domain == "" {
		return "", true
	}
	normalized := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(domain)), ".")
	if normalized == "" || strings.HasPrefix(normalized, ".") || strings.HasSuffix(normalized, ".") ||
		strings.ContainsAny(normalized, " \t\r\n/\\:") {
		return "", false
	}
	ascii, err := idna.ToASCII(normalized)
	return ascii, err == nil && ascii != ""
}

func isPublicCookieSuffix(domain string) bool {
	if net.ParseIP(domain) != nil {
		return false
	}
	return publicsuffix.List.PublicSuffix(domain) == domain
}

func defaultCookiePath(path string) string {
	if lastSlash := strings.LastIndex(path, "/"); lastSlash > 0 {
		return path[:lastSlash]
	}
	return "/"
}

func normalizeCookiePath(path string) string {
	if !strings.HasPrefix(path, "/") {
		return "/"
	}
	return path
}

func newSessionCookie(cookie *http.Cookie) (sessionCookie, bool) {
	if cookie == nil || cookie.Name == "" {
		return sessionCookie{}, false
	}
	domain, ok := normalizeCookieDomain(cookie.Domain)
	if !ok {
		return sessionCookie{}, false
	}
	entry := sessionCookie{Cookie: *cookie}
	entry.Domain = domain
	entry.Path = normalizeCookiePath(cookie.Path)
	entry.Unparsed = nil
	return entry, true
}

func (entry sessionCookie) isMirrorAVS() bool {
	return isRootAVS(&entry.Cookie) && entry.Domain == "" && entry.hostOnly == ""
}

func isRootAVS(cookie *http.Cookie) bool {
	return cookie.Name == "AVS" && cookie.Path == "/"
}

func (c *Client) isCookieMirrorLocked(origin cookieOrigin) bool {
	if base, ok := parseBaseURL(c.baseURL); ok && cookieOriginOf(base) == origin {
		return true
	}
	for _, raw := range c.baseURLs {
		if base, ok := parseBaseURL(raw); ok && cookieOriginOf(base) == origin {
			return true
		}
	}
	return false
}

// SetLoginAVS 保存最终登录响应的 AVS，供该 origin 和配置内镜像使用
func (c *Client) SetLoginAVS(source *url.URL, value string) {
	entry := mirrorAVSCookie(value)
	entry.loginOrigin = cookieOriginOf(source)
	c.mergeSessionCookies([]sessionCookie{entry}, time.Now())
}

// CookieValue 返回该 URL 上实际会先发送的同名 Cookie 值
func (c *Client) CookieValue(u *url.URL, name string) string {
	for _, cookie := range (sessionCookieJar{c}).Cookies(u) {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

func mirrorAVSCookie(value string) sessionCookie {
	entry := sessionCookie{Cookie: http.Cookie{Name: "AVS", Value: value, Path: "/"}}
	if value == "" {
		entry.MaxAge = -1
	}
	return entry
}

func (c *Client) SetCookies(cookies []*http.Cookie) {
	base, _ := parseBaseURL(c.BaseURL())
	var host string
	if base != nil {
		host = canonicalCookieHost(base.Hostname())
	}
	entries := make([]sessionCookie, 0, len(cookies))
	for _, cookie := range cookies {
		entry, ok := newSessionCookie(cookie)
		if !ok {
			continue
		}
		if entry.Domain == "" && !entry.isMirrorAVS() {
			entry.hostOnly = host
		}
		entries = append(entries, entry)
	}
	c.replaceCookies(mergeCookies(nil, entries, time.Now()))
	c.saveCookies()
}

func (c *Client) GetCookies() []*http.Cookie {
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()

	var cookies []*http.Cookie
	for _, entry := range c.cookies {
		cookie := entry.Cookie
		cookies = append(cookies, &cookie)
	}
	return cookies
}

func (c *Client) replaceCookies(cookies []sessionCookie) {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()

	c.cookies = cookies
}

func (c *Client) mergeSessionCookies(entries []sessionCookie, now time.Time) {
	if len(entries) == 0 {
		return
	}

	c.sessionMu.Lock()
	c.cookies = mergeCookies(c.cookies, entries, now)
	c.sessionMu.Unlock()
	c.saveCookies()
}

func (c *Client) saveCookies() {
	if err := c.SaveCookies(); err != nil {
		logger.Warn("保存 cookies 失败", "error", err)
	}
}

type cookieValidationPolicy struct {
	primaryHost  string
	allowedHosts []string
	hasHTTP      bool
	hasHTTPS     bool
}

func (c *Client) cookieValidationPolicy() cookieValidationPolicy {
	var policy cookieValidationPolicy
	baseURLs, _ := c.requestBaseURLs()
	for _, base := range baseURLs {
		u, ok := parseBaseURL(base)
		if !ok {
			continue
		}
		origin := cookieOriginOf(u)
		if origin.host == "" {
			continue
		}
		if policy.primaryHost == "" {
			policy.primaryHost = origin.host
		}
		policy.allowedHosts = append(policy.allowedHosts, origin.host)
		policy.hasHTTP = policy.hasHTTP || origin.scheme == "http"
		policy.hasHTTPS = policy.hasHTTPS || origin.scheme == "https"
	}
	return policy
}

func filterValidCookies(cookieData []CookieData, now time.Time, policy cookieValidationPolicy) []sessionCookie {
	entries := make([]sessionCookie, 0, len(cookieData))
	for _, cd := range cookieData {
		if !cd.Expires.IsZero() && !cd.Expires.After(now) || cd.Secure && policy.hasHTTP && !policy.hasHTTPS {
			continue
		}

		entry, ok := newSessionCookie(&http.Cookie{
			Name: cd.Name, Value: cd.Value, Path: cd.Path, Domain: cd.Domain,
			Expires: cd.Expires, MaxAge: cd.MaxAge, Secure: cd.Secure, HttpOnly: cd.HTTPOnly,
		})
		if !ok {
			continue
		}
		if cd.LoginOrigin != "" {
			u, err := url.Parse(cd.LoginOrigin)
			if err != nil || !entry.isMirrorAVS() || cd.HostOnly != "" {
				continue
			}
			entry.loginOrigin = cookieOriginOf(u)
			if entry.loginOrigin.cacheKey() != cd.LoginOrigin {
				continue
			}
		}
		switch {
		case cd.HostOnly != "":
			entry.hostOnly = canonicalCookieHost(cd.HostOnly)
			if entry.hostOnly == "" || entry.Domain != "" {
				continue
			}
		case entry.Domain == "" && !entry.isMirrorAVS():
			// 旧文件不记录原始来源，只能将普通空 Domain Cookie 绑定到主镜像
			entry.hostOnly = policy.primaryHost
		case entry.Domain != "" && isPublicCookieSuffix(entry.Domain):
			if entry.Domain != policy.primaryHost {
				continue
			}
			entry.hostOnly, entry.Domain = entry.Domain, ""
		}
		if !policy.allows(entry) {
			continue
		}
		entries = append(entries, entry)
	}
	return mergeCookies(nil, entries, now)
}

func (p cookieValidationPolicy) allows(entry sessionCookie) bool {
	scope := entry.hostOnly
	if scope == "" {
		scope = entry.Domain
	}
	if scope == "" || len(p.allowedHosts) == 0 {
		return true
	}
	for _, host := range p.allowedHosts {
		if cookieDomainMatches(scope, host) || entry.Domain != "" && cookieDomainMatches(host, scope) {
			return true
		}
	}
	return false
}

func normalizeResponseCookieExpiry(cookie *http.Cookie, receivedAt time.Time) {
	// 只在收到响应时换算，避免读取缓存时重新开始计时
	if cookie.MaxAge > 0 {
		const maxAgeSeconds = math.MaxInt64 / int64(time.Second)
		seconds := min(int64(cookie.MaxAge), maxAgeSeconds)
		cookie.Expires = receivedAt.Add(time.Duration(seconds) * time.Second)
		cookie.MaxAge = 0
	}
}

func isDeletionCookie(cookie *http.Cookie, now time.Time) bool {
	if cookie.MaxAge < 0 {
		return true
	}
	return cookie.MaxAge == 0 && !cookie.Expires.IsZero() && !cookie.Expires.After(now)
}

type cookieIdentity struct {
	domain string
	path   string
	name   string
}

func (entry sessionCookie) key() cookieIdentity {
	domain := entry.Domain
	if domain == "" {
		domain = entry.hostOnly
	}
	return cookieIdentity{domain: domain, path: entry.Path, name: entry.Name}
}

// 上游会重发登录 Cookie，替换同一作用域的值避免请求头随请求次数增长
func mergeCookies(existing, incoming []sessionCookie, now time.Time) []sessionCookie {
	merged := make([]sessionCookie, 0, len(existing)+len(incoming))
	index := make(map[cookieIdentity]int, len(existing)+len(incoming))
	for _, entry := range existing {
		index[entry.key()] = len(merged)
		merged = append(merged, entry)
	}
	for _, entry := range incoming {
		key := entry.key()
		pos, exists := index[key]
		if isDeletionCookie(&entry.Cookie, now) {
			if exists {
				merged[pos] = sessionCookie{}
				delete(index, key)
			}
			continue
		}
		if exists {
			merged[pos] = entry
			continue
		}
		index[key] = len(merged)
		merged = append(merged, entry)
	}

	out := merged[:0]
	for _, entry := range merged {
		if entry.Name != "" && !isDeletionCookie(&entry.Cookie, now) {
			out = append(out, entry)
		}
	}
	return out
}

// 登录态靠这几个 cookie，宁可超一点也不能丢
var essentialCookieNames = map[string]struct{}{
	"AVS":         {},
	"remember":    {},
	"remember_id": {},
}

func cookieSize(cookie *http.Cookie) int {
	// name=value 再加上 "; "
	return len(cookie.Name) + len(cookie.Value) + 3
}

func cappedCookies(cookies []*http.Cookie) []*http.Cookie {
	total := 0
	for _, cookie := range cookies {
		total += cookieSize(cookie)
	}
	if total <= maxCookieHeaderBytes {
		return cookies
	}

	budget := maxCookieHeaderBytes
	for _, cookie := range cookies {
		if _, ok := essentialCookieNames[cookie.Name]; ok {
			budget -= cookieSize(cookie)
		}
	}

	kept := make([]*http.Cookie, 0, len(cookies))
	for _, cookie := range cookies {
		if _, ok := essentialCookieNames[cookie.Name]; ok {
			kept = append(kept, cookie)
			continue
		}
		size := cookieSize(cookie)
		if size > budget {
			continue
		}
		budget -= size
		kept = append(kept, cookie)
	}

	logger.Warn("Cookie 请求头超出上限，已丢弃非必要 cookie",
		"kept", len(kept), "dropped", len(cookies)-len(kept), "limit_bytes", maxCookieHeaderBytes)
	return kept
}
