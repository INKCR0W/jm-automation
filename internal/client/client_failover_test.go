package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestGetFailsOverOnGatewayStatus(t *testing.T) {
	tests := []struct {
		status       int
		wantFallback bool
	}{
		{http.StatusBadGateway, true},
		{http.StatusServiceUnavailable, true},
		{http.StatusGatewayTimeout, true},
		{http.StatusInternalServerError, false},
		{http.StatusTooManyRequests, false},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			primary := newFailoverTestServer(t, tt.status, "primary")
			var fallbackCalls atomic.Int32
			backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fallbackCalls.Add(1)
				_, _ = io.WriteString(w, "backup")
			}))
			t.Cleanup(backup.Close)
			c := newFailoverTestClient(t, primary.URL, backup.URL)

			resp, err := c.Get(context.Background(), "/favorite", nil)
			if err != nil {
				t.Fatal(err)
			}
			wantStatus, wantBody, wantBaseURL := tt.status, "primary", primary.URL
			var wantCalls int32
			if tt.wantFallback {
				wantStatus, wantBody, wantBaseURL = http.StatusOK, "backup", backup.URL
				wantCalls = 1
			}
			if resp.StatusCode != wantStatus || string(resp.Body) != wantBody {
				t.Fatalf("response = %d %q, want %d %q", resp.StatusCode, resp.Body, wantStatus, wantBody)
			}
			if c.BaseURL() != wantBaseURL || fallbackCalls.Load() != wantCalls {
				t.Fatalf("base URL = %s, backup calls = %d, want %s and %d", c.BaseURL(), fallbackCalls.Load(), wantBaseURL, wantCalls)
			}
		})
	}
}

func TestGetPreservesLastFailedGatewayResponse(t *testing.T) {
	primary := newFailoverTestServer(t, http.StatusBadGateway, "bad gateway")
	backup := newFailoverTestServer(t, http.StatusGatewayTimeout, "gateway timeout")
	c := newFailoverTestClient(t, primary.URL, backup.URL)

	resp, err := c.Get(context.Background(), "/favorite", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusGatewayTimeout || string(resp.Body) != "gateway timeout" {
		t.Fatalf("response = %d %q, want last gateway failure", resp.StatusCode, resp.Body)
	}
	if got := c.BaseURL(); got != primary.URL {
		t.Fatalf("base URL = %s, want unchanged %s", got, primary.URL)
	}
}

func TestGetReturnsBackupHTTPErrorWithoutPromotingIt(t *testing.T) {
	primary := newFailoverTestServer(t, http.StatusBadGateway, "bad gateway")
	backup := newFailoverTestServer(t, http.StatusTooManyRequests, "rate limited")
	c := newFailoverTestClient(t, primary.URL, backup.URL)

	resp, err := c.Get(t.Context(), "/favorite", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusTooManyRequests || string(resp.Body) != "rate limited" {
		t.Fatalf("response = %d %q, want the backup's error", resp.StatusCode, resp.Body)
	}
	if got := c.BaseURL(); got != primary.URL {
		t.Fatalf("base URL = %s, want %s", got, primary.URL)
	}
}

func TestConfirmBaseURLCannotRestoreRemovedCandidate(t *testing.T) {
	primary := newFailoverTestServer(t, http.StatusBadGateway, "bad gateway")
	backup := newFailoverTestServer(t, http.StatusOK, "backup")
	c := newFailoverTestClient(t, primary.URL, backup.URL)

	resp, err := c.GetWithToken(t.Context(), "/favorite", 123, "2.0.20")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("request = %v, %v, want successful backup response", resp, err)
	}
	if got := c.BaseURL(); got != primary.URL {
		t.Fatalf("unconfirmed base URL = %s, want %s", got, primary.URL)
	}
	c.SetBaseURLs([]string{primary.URL})
	c.ConfirmBaseURL(resp)
	if got := c.BaseURL(); got != primary.URL {
		t.Fatalf("base URL after confirming stale response = %s, want %s", got, primary.URL)
	}
}

func TestPostFormDoesNotReplayGatewayResponse(t *testing.T) {
	primary := newFailoverTestServer(t, http.StatusServiceUnavailable, "unavailable")
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected replay of %s to backup domain", r.Method)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backup.Close)
	c := newFailoverTestClient(t, primary.URL, backup.URL)

	resp, err := c.PostForm(context.Background(), "/daily_chk", map[string]string{"daily_id": "1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable || string(resp.Body) != "unavailable" {
		t.Fatalf("response = %d %q, want original POST failure", resp.StatusCode, resp.Body)
	}
	if got := c.BaseURL(); got != primary.URL {
		t.Fatalf("base URL = %s, want unchanged %s", got, primary.URL)
	}
}

func newFailoverTestServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server
}

func newFailoverTestClient(t *testing.T, primary, backup string) *Client {
	t.Helper()
	c, err := NewEphemeral(primary, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.SetBaseURLs([]string{primary, backup})
	return c
}
