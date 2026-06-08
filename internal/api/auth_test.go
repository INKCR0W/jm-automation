package api

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/INKCR0W/jm-automation/internal/client"
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

	authAPI.applyLoginSession(loginData)

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
	c, err := client.New("https://example.test", time.Second, username)
	if err != nil {
		t.Fatalf("new client failed: %v", err)
	}

	return c
}
