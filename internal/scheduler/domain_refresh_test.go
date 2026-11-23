package scheduler

import (
	"bytes"
	"crypto/aes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/INKCR0W/jm-automation/internal/api"
	"github.com/INKCR0W/jm-automation/internal/client"
	"github.com/INKCR0W/jm-automation/internal/config"
	"github.com/INKCR0W/jm-automation/pkg/crypto"
)

func TestRefreshKeepsWorkingDomainUntilNewCandidatePassesProbe(t *testing.T) {
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/check-route" {
			http.NotFound(w, r)
			return
		}
		writeDomainFixture(t, w, "working route")
	}))
	t.Cleanup(old.Close)

	var newRouteLive atomic.Bool
	var probes atomic.Int32
	var candidates []string
	// Fill the probe budget with loopback candidates so no built-in domain is contacted
	for i := range 5 {
		candidate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case api.PathSetting:
				probes.Add(1)
				if i != 0 || !newRouteLive.Load() {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				ts := r.URL.Query().Get("t")
				data := encryptDomainFixture(t, []byte(`{"img_host":"https://img.example.test"}`), ts+crypto.AppDataSecret)
				if err := json.NewEncoder(w).Encode(api.EncryptedResponse{Code: api.CodeSuccess, Data: data}); err != nil {
					t.Errorf("write setting fixture: %v", err)
				}
			default:
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(candidate.Close)
		candidates = append(candidates, candidate.URL)
	}

	var primaryCalls, backupCalls atomic.Int32
	primaryMirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryCalls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(primaryMirror.Close)
	configJSON, err := json.Marshal(map[string][]string{"Server": candidates})
	if err != nil {
		t.Fatal(err)
	}
	configBody := encryptDomainFixture(t, configJSON, api.HostConfigSecret)
	backupMirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		backupCalls.Add(1)
		writeDomainFixture(t, w, configBody)
	}))
	t.Cleanup(backupMirror.Close)
	previousMirrors := api.HostConfigURLs
	api.HostConfigURLs = []string{primaryMirror.URL, backupMirror.URL}
	t.Cleanup(func() { api.HostConfigURLs = previousMirrors })

	s := &Scheduler{
		config:          &config.Config{Server: config.ServerConfig{Timeout: 2}},
		clientMap:       make(map[string]*client.Client),
		baseURLs:        []string{old.URL},
		resolveBaseURLs: api.Resolve,
	}
	t.Chdir(t.TempDir())
	c, err := s.getOrCreateClient("cached-account")
	if err != nil {
		t.Fatal(err)
	}
	if runErr := s.RunOnce(t.Context()); runErr != nil {
		t.Fatal(runErr)
	}
	if primaryCalls.Load() != 1 || backupCalls.Load() != 1 || probes.Load() != int32(len(candidates)) {
		t.Fatalf("mirror requests = (%d, %d), probes = %d; want (1, 1), %d",
			primaryCalls.Load(), backupCalls.Load(), probes.Load(), len(candidates))
	}
	newClient, err := s.getOrCreateClient("new-account")
	if err != nil {
		t.Fatal(err)
	}
	for _, account := range []*client.Client{c, newClient} {
		if account.BaseURL() != old.URL {
			t.Fatalf("preferred URL after failed probes = %q, want %q", account.BaseURL(), old.URL)
		}
		resp, err := account.Get(t.Context(), "/check-route", nil)
		if err != nil || resp.StatusCode != http.StatusOK || string(resp.Body) != "working route" {
			t.Fatalf("request to previous route = %v, %v, want working route", resp, err)
		}
	}

	newRouteLive.Store(true)
	if runErr := s.RunOnce(t.Context()); runErr != nil {
		t.Fatal(runErr)
	}
	if probes.Load() != int32(len(candidates)+1) || c.BaseURL() != candidates[0] || newClient.BaseURL() != candidates[0] {
		t.Fatalf("successful refresh added %d probes and picked (%q, %q), want one probe and %q",
			probes.Load()-int32(len(candidates)), c.BaseURL(), newClient.BaseURL(), candidates[0])
	}
}

func encryptDomainFixture(t *testing.T, body []byte, secret string) string {
	t.Helper()
	block, err := aes.NewCipher([]byte(crypto.MD5Hex(secret)))
	if err != nil {
		t.Fatal(err)
	}
	padding := aes.BlockSize - len(body)%aes.BlockSize
	padded := append(append([]byte(nil), body...), bytes.Repeat([]byte{byte(padding)}, padding)...)
	for offset := 0; offset < len(padded); offset += aes.BlockSize {
		block.Encrypt(padded[offset:offset+aes.BlockSize], padded[offset:offset+aes.BlockSize])
	}
	return base64.StdEncoding.EncodeToString(padded)
}

func writeDomainFixture(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()
	if _, err := io.WriteString(w, body); err != nil {
		t.Errorf("write domain fixture: %v", err)
	}
}
