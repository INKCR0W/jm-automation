package api

import (
	"context"
	"crypto/aes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/INKCR0W/jm-automation/pkg/crypto"
)

func TestHostConfigDomainsIncludesJM3Server(t *testing.T) {
	raw := `{"Server":["www.a.net","www.b.cc"],"jm3_Server":[["www.a.net","線路1"],["www.c.me","線路5"],[]]}`

	var cfg hostConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unmarshal returned error: %v", err)
	}

	got := cfg.domains()
	want := []string{"www.a.net", "www.b.cc", "www.a.net", "www.c.me"}
	if len(got) != len(want) {
		t.Fatalf("domains = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("domains = %v, want %v", got, want)
		}
	}
}

func settingServer(t *testing.T) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != PathSetting {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		tokenparam := r.Header.Get("tokenparam")
		if tokenparam == "" || r.Header.Get("token") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		ts := r.URL.Query().Get("t")
		key := crypto.MD5Hex(ts + crypto.AppDataSecret)
		payload := encryptECB(t, []byte(`{"img_host":"https://img.example.net"}`), []byte(key))

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(EncryptedResponse{Code: CodeSuccess, Data: payload}); err != nil {
			t.Errorf("encode setting response failed: %v", err)
		}
	}))
}

func encryptECB(t *testing.T, plaintext, key []byte) string {
	t.Helper()

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("new cipher failed: %v", err)
	}

	padding := aes.BlockSize - len(plaintext)%aes.BlockSize
	for range padding {
		plaintext = append(plaintext, byte(padding))
	}

	ciphertext := make([]byte, len(plaintext))
	for i := 0; i < len(plaintext); i += aes.BlockSize {
		block.Encrypt(ciphertext[i:i+aes.BlockSize], plaintext[i:i+aes.BlockSize])
	}
	return base64.StdEncoding.EncodeToString(ciphertext)
}

func TestPromoteLiveBaseURLMovesWorkingDomainToFront(t *testing.T) {
	live := settingServer(t)
	defer live.Close()

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=iso-8859-1")
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer dead.Close()

	candidates := []string{dead.URL, live.URL, "https://other.example.net"}
	got, gotLive := promoteLiveBaseURL(context.Background(), candidates)

	if !gotLive {
		t.Fatal("promoteLiveBaseURL reported no live candidate")
	}
	if got[0] != live.URL {
		t.Fatalf("primary = %s, want %s", got[0], live.URL)
	}
	if len(got) != len(candidates) {
		t.Fatalf("candidate count = %d, want %d", len(got), len(candidates))
	}
	// 探挂的要排在没试过的后面，否则回退会先去撞已知的死域名、白等超时
	if got[1] != "https://other.example.net" || got[2] != dead.URL {
		t.Fatalf("candidates = %v, want untried before the failed one", got)
	}
}

func TestPromoteLiveBaseURLKeepsOrderWhenFirstIsLive(t *testing.T) {
	live := settingServer(t)
	defer live.Close()

	candidates := []string{live.URL, "https://other.example.net"}
	got, gotLive := promoteLiveBaseURL(context.Background(), candidates)

	if !gotLive {
		t.Fatal("promoteLiveBaseURL reported no live candidate")
	}
	if got[0] != live.URL || len(got) != 2 {
		t.Fatalf("candidates = %v, want %v unchanged", got, candidates)
	}
}

func TestPromoteLiveBaseURLFallsBackToOriginalOrder(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer dead.Close()

	candidates := []string{dead.URL, dead.URL + "/x"}
	got, gotLive := promoteLiveBaseURL(context.Background(), candidates)

	if gotLive {
		t.Fatal("promoteLiveBaseURL reported a live candidate when every probe failed")
	}
	if len(got) != len(candidates) || got[0] != candidates[0] {
		t.Fatalf("candidates = %v, want %v unchanged", got, candidates)
	}
}

func TestPromoteLiveBaseURLStopsAtProbeLimit(t *testing.T) {
	probes := 0
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		probes++
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer dead.Close()

	candidates := make([]string, 0, maxProbedCandidates+3)
	for i := range maxProbedCandidates + 3 {
		candidates = append(candidates, fmt.Sprintf("%s/%d", dead.URL, i))
	}

	got, gotLive := promoteLiveBaseURL(context.Background(), candidates)

	if gotLive {
		t.Fatal("promoteLiveBaseURL reported a live candidate when every probe failed")
	}
	if probes != maxProbedCandidates {
		t.Fatalf("probes = %d, want %d", probes, maxProbedCandidates)
	}
	if len(got) != len(candidates) {
		t.Fatalf("candidate count = %d, want %d", len(got), len(candidates))
	}
}

func TestProbeSettingRejectsHTMLResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=iso-8859-1")
		if _, err := w.Write([]byte("<!DOCTYPE HTML><html><title>400 Bad Request</title></html>")); err != nil {
			t.Errorf("write failed: %v", err)
		}
	}))
	defer srv.Close()

	if err := probeSetting(context.Background(), srv.URL); err == nil {
		t.Fatal("probeSetting returned nil error for an HTML response")
	}
}

func TestResolutionDegraded(t *testing.T) {
	tests := []struct {
		name   string
		res    Resolution
		wantIt bool
	}{
		{"啥都没拿到", Resolution{}, true},
		{"远程配置拿到了", Resolution{Remote: true}, false},
		{"探活通了", Resolution{Live: true}, false},
		{"都拿到了", Resolution{Remote: true, Live: true}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.res.Degraded(); got != tt.wantIt {
				t.Fatalf("Degraded() = %v, want %v", got, tt.wantIt)
			}
		})
	}
}
