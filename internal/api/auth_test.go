package api

import (
	"bytes"
	"context"
	"crypto/aes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/INKCR0W/jm-automation/internal/client"
	"github.com/INKCR0W/jm-automation/pkg/crypto"
	fhttp "github.com/bogdanfinn/fhttp"
)

func TestParseLoginResponse(t *testing.T) {
	tests := []struct {
		name          string
		plaintext     string
		inputUsername string
		wantUsername  string
		wantUID       string
		wantAVS       string
		wantJWT       string
		wantErr       string
	}{
		{
			name:          "保留响应中的用户名和会话字段",
			plaintext:     `{"uid":"42","username":"alice","nickname":"ignored","s":"avs-token","jwttoken":"jwt-token","level":3}`,
			inputUsername: "login-name",
			wantUsername:  "alice",
			wantUID:       "42",
			wantAVS:       "avs-token",
			wantJWT:       "jwt-token",
		},
		{
			name:          "用户名为空时回退到昵称",
			plaintext:     `{"uid":"42","nickname":"nick","s":"avs-token"}`,
			inputUsername: "login-name",
			wantUsername:  "nick",
			wantUID:       "42",
			wantAVS:       "avs-token",
		},
		{
			name:          "用户名和昵称都为空时回退到输入用户名",
			plaintext:     `{"uid":"42","s":"avs-token"}`,
			inputUsername: "login-name",
			wantUsername:  "login-name",
			wantUID:       "42",
			wantAVS:       "avs-token",
		},
		{
			name:          "缺少用户ID时报错",
			plaintext:     `{"username":"alice","s":"avs-token"}`,
			inputUsername: "login-name",
			wantErr:       "用户ID",
		},
		{
			name:          "非法JSON时报错",
			plaintext:     `{invalid`,
			inputUsername: "login-name",
			wantErr:       "解析登录数据失败",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseLoginResponse(tt.plaintext, tt.inputUsername)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("parseLoginResponse() error = nil, want contains %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseLoginResponse() error = %q, want contains %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseLoginResponse() error = %v", err)
			}
			if got.UID != tt.wantUID {
				t.Fatalf("UID = %q, want %q", got.UID, tt.wantUID)
			}
			if got.Username != tt.wantUsername {
				t.Fatalf("Username = %q, want %q", got.Username, tt.wantUsername)
			}
			if got.S != tt.wantAVS {
				t.Fatalf("S = %q, want %q", got.S, tt.wantAVS)
			}
			if got.JWTToken != tt.wantJWT {
				t.Fatalf("JWTToken = %q, want %q", got.JWTToken, tt.wantJWT)
			}
		})
	}
}

func TestApplyLoginSessionStoresAuthState(t *testing.T) {
	c := newAuthTestClient(t)
	authAPI := NewAuthAPI(c)

	loginData := LoginData{
		UID:      "42",
		Username: "alice",
		S:        "avs-token",
		JWTToken: "jwt-token",
	}

	authAPI.applyLoginSession(loginData, nil)

	if got := authAPI.GetUserID(); got != "42" {
		t.Fatalf("AuthAPI userID = %q, want 42", got)
	}
	if got := authAPI.GetAVS(); got != "avs-token" {
		t.Fatalf("AuthAPI avs = %q, want avs-token", got)
	}
	if got := c.GetJWTToken(); got != "jwt-token" {
		t.Fatalf("client JWT = %q, want jwt-token", got)
	}
	if got := c.GetUserID(); got != "42" {
		t.Fatalf("client userID = %q, want 42", got)
	}
	if got := c.GetUsername(); got != "alice" {
		t.Fatalf("client username = %q, want alice", got)
	}

	cookies := c.GetCookies()
	for _, cookie := range cookies {
		if cookie.Name == "AVS" && cookie.Value == "avs-token" {
			return
		}
	}
	t.Fatalf("client cookies missing AVS=avs-token: %#v", cookies)
}

func newAuthTestClient(t *testing.T) *client.Client {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}

	tmpDir := t.TempDir()
	if chdirErr := os.Chdir(tmpDir); chdirErr != nil {
		t.Fatalf("chdir temp dir failed: %v", chdirErr)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	username := fmt.Sprintf("auth_test_%d", time.Now().UnixNano())
	c, err := client.New(t.Context(), "https://example.test", time.Second, username)
	if err != nil {
		t.Fatalf("new client failed: %v", err)
	}

	return c
}

func TestLoginReplacesStoredAVSWithLoginBody(t *testing.T) {
	for _, tc := range []struct {
		name         string
		hasDomainAVS bool
		hasWireAVS   bool
	}{
		{name: "stored host-only AVS"},
		{name: "stored explicit Domain AVS", hasDomainAVS: true},
		{name: "AVS also set by login response", hasWireAVS: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/seed":
					http.SetCookie(w, &http.Cookie{Name: "AVS", Value: "stale", Path: "/"})
				case PathLogin:
					http.Redirect(w, r, "/auth/complete", http.StatusTemporaryRedirect)
				case "/auth/complete":
					if r.Method != http.MethodPost {
						t.Errorf("login method = %q, want POST", r.Method)
					}
					if tc.hasWireAVS {
						http.SetCookie(w, &http.Cookie{Name: "AVS", Value: "wire", Path: "/", HttpOnly: true})
					}
					writeTestLoginResponse(t, w, r)
				case "/cookie":
					_, _ = io.WriteString(w, r.Header.Get("Cookie"))
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)

			c, err := client.NewEphemeral(t.Context(), server.URL, 2*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if tc.hasDomainAVS {
				origin, parseErr := url.Parse(server.URL)
				if parseErr != nil {
					t.Fatal(parseErr)
				}
				c.SetCookies([]*fhttp.Cookie{{Name: "AVS", Value: "stale", Path: "/", Domain: origin.Hostname()}})
			} else if _, seedErr := c.Get(ctx, "/seed", nil); seedErr != nil {
				t.Fatal(seedErr)
			}
			auth := NewAuthAPI(c)
			if _, loginErr := auth.Login(ctx, "test-user", "test-password"); loginErr != nil {
				t.Fatal(loginErr)
			}
			resp, err := c.Get(ctx, "/cookie", nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := string(resp.Body); got != "AVS=body" {
				t.Fatalf("Cookie = %q, want AVS=body", got)
			}
			if tc.hasWireAVS {
				for _, cookie := range c.GetCookies() {
					if cookie.Name == "AVS" && cookie.Value == "wire" && cookie.HttpOnly {
						return
					}
				}
				t.Fatal("login body changed the wire cookie or its HttpOnly attribute")
			}
		})
	}
}

func writeTestLoginResponse(t *testing.T, w http.ResponseWriter, req *http.Request) {
	t.Helper()
	ts, _, ok := strings.Cut(req.Header.Get("tokenparam"), ",")
	if !ok {
		t.Error("login request is missing tokenparam")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	block, err := aes.NewCipher([]byte(crypto.MD5Hex(ts + crypto.AppDataSecret)))
	if err != nil {
		t.Errorf("create login response cipher: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	payload := []byte("{\"uid\":\"42\",\"username\":\"test-user\",\"s\":\"body\"}")
	padding := aes.BlockSize - len(payload)%aes.BlockSize
	payload = append(payload, bytes.Repeat([]byte{byte(padding)}, padding)...)
	for offset := 0; offset < len(payload); offset += aes.BlockSize {
		block.Encrypt(payload[offset:offset+aes.BlockSize], payload[offset:offset+aes.BlockSize])
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"code": 200,
		"data": base64.StdEncoding.EncodeToString(payload),
	}); err != nil {
		t.Errorf("write login response: %v", err)
	}
}
