package api

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/INKCR0W/jm-automation/internal/client"
	"github.com/INKCR0W/jm-automation/pkg/crypto"
)

func TestLoginRoutesByConfirmedCachedSession(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      int
		body        string
		jsonGateway bool
		usesCache   bool
	}{
		{name: "rate limited", status: http.StatusTooManyRequests, body: `{"code":429}`},
		{name: "unauthorized", status: http.StatusUnauthorized, body: `{"code":401}`},
		{name: "server error", status: http.StatusInternalServerError, body: `{"code":500}`},
		{name: "business rejection", status: http.StatusOK, body: `{"code":403,"errorMsg":"session expired"}`},
		{name: "damaged ciphertext", status: http.StatusOK, body: `{"code":200,"data":"bad!"}`},
		{name: "JSON gateway error followed by rate limit", status: http.StatusTooManyRequests, body: `{"code":429}`, jsonGateway: true},
		{name: "cached session accepted", status: http.StatusOK, usesCache: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var primaryChecks, backupChecks, primaryLogins, backupLogins atomic.Int32
			primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == PathFavorite:
					primaryChecks.Add(1)
					if tc.jsonGateway {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusServiceUnavailable)
						writeRoutingResponse(t, w, `{"code":503}`)
						return
					}
					w.Header().Set("Content-Type", "text/html")
					w.WriteHeader(http.StatusBadGateway)
					writeRoutingResponse(t, w, "<html>temporary gateway error</html>")
				case r.Method == http.MethodPost && r.URL.Path == PathLogin:
					primaryLogins.Add(1)
					if err := r.ParseForm(); err != nil || r.Form.Get("username") != "test-user" ||
						r.Form.Get("password") != "test-password" || r.Form.Get("login_remember") != "on" {
						t.Error("credential request did not carry the expected form")
					}
					writeEncryptedRoutingResponse(t, w, r, `{"uid":"fresh-user","username":"test-user","s":"avs"}`)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(primary.Close)
			backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == PathFavorite:
					backupChecks.Add(1)
					if tc.usesCache {
						writeEncryptedRoutingResponse(t, w, r, `{"list":[]}`)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tc.status)
					writeRoutingResponse(t, w, tc.body)
				case r.Method == http.MethodPost && r.URL.Path == PathLogin:
					backupLogins.Add(1)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusServiceUnavailable)
					writeRoutingResponse(t, w, `{"code":503,"errorMsg":"backup login unavailable"}`)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(backup.Close)

			c, err := client.NewEphemeral(primary.URL, 2*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			c.SetBaseURLs([]string{primary.URL, backup.URL})
			c.SetJWTToken("fixture-jwt")
			c.SetUserInfo("cached-user", "test-user")

			result, err := NewAuthAPI(c).Login(t.Context(), "test-user", "test-password")
			if err != nil {
				t.Fatalf("login failed (primary POSTs=%d, backup POSTs=%d): %v", primaryLogins.Load(), backupLogins.Load(), err)
			}
			wantUID, wantBaseURL, wantLogins := "fresh-user", primary.URL, int32(1)
			if tc.usesCache {
				wantUID, wantBaseURL, wantLogins = "cached-user", backup.URL, 0
			}
			if result.UID != wantUID || c.BaseURL() != wantBaseURL {
				t.Fatalf("login UID and preferred URL = %q, %q; want %q, %q", result.UID, c.BaseURL(), wantUID, wantBaseURL)
			}
			if primaryChecks.Load() != 1 || backupChecks.Load() != 1 || primaryLogins.Load() != wantLogins || backupLogins.Load() != 0 {
				t.Fatalf("GETs (primary, backup) = (%d, %d), POSTs = (%d, %d); want (1, 1), (%d, 0)",
					primaryChecks.Load(), backupChecks.Load(), primaryLogins.Load(), backupLogins.Load(), wantLogins)
			}
		})
	}
}

func writeEncryptedRoutingResponse(t *testing.T, w http.ResponseWriter, r *http.Request, plaintext string) {
	t.Helper()
	ts, _, ok := strings.Cut(r.Header.Get("tokenparam"), ",")
	if !ok {
		t.Error("request is missing tokenparam")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	data := encryptECB(t, []byte(plaintext), []byte(crypto.MD5Hex(ts+crypto.AppDataSecret)))
	w.Header().Set("Content-Type", "application/json")
	writeRoutingResponse(t, w, fmt.Sprintf(`{"code":200,"data":%q}`, data))
}

func writeRoutingResponse(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()
	if _, err := io.WriteString(w, body); err != nil {
		t.Errorf("write fixture response: %v", err)
	}
}
