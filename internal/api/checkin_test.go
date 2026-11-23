package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/INKCR0W/jm-automation/internal/client"
	"github.com/INKCR0W/jm-automation/pkg/crypto"
)

func TestDailyCheckInResponse(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		plaintext string
		wantErr   string
		wantMsg   string
	}{
		{name: "缺少 data", status: http.StatusOK, body: `{"code":200}`},
		{name: "data 为空字符串", status: http.StatusOK, body: `{"code":200,"data":""}`},
		{name: "data 为 null", status: http.StatusOK, body: `{"code":200,"data":null}`},
		{name: "HTTP 失败", status: http.StatusForbidden, body: `{"code":200,"data":""}`, wantErr: "status=403"},
		{name: "业务失败", status: http.StatusOK, body: `{"code":403,"data":"","errorMsg":"签到被拒绝"}`, wantErr: "签到被拒绝"},
		{name: "加密数据损坏", status: http.StatusOK, body: `{"code":200,"data":"invalid!"}`, wantErr: "解密响应数据失败"},
		{name: "非空响应", status: http.StatusOK, plaintext: `{"msg":"今天已经签到过了"}`, wantMsg: "今天已经签到过了"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != PathDailyChk {
					t.Errorf("收到非预期请求 %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
					return
				}

				body := tc.body
				if tc.plaintext != "" {
					ts, _, _ := strings.Cut(r.Header.Get("tokenparam"), ",")
					key := crypto.MD5Hex(ts + crypto.AppDataSecret)
					data := encryptECB(t, []byte(tc.plaintext), []byte(key))
					body = fmt.Sprintf(`{"code":200,"data":%q}`, data)
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				if _, err := w.Write([]byte(body)); err != nil {
					t.Errorf("写入响应失败: %v", err)
				}
			}))
			defer srv.Close()

			c, err := client.NewEphemeral(t.Context(), srv.URL, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			result, err := NewCheckInAPI(c, "test-user").DailyCheckIn(context.Background(), "test-daily")
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || result == nil || !result.Success || result.Msg != tc.wantMsg {
				t.Fatalf("result = %+v, err = %v, want success with msg %q", result, err, tc.wantMsg)
			}
		})
	}
}

func TestDecryptResponseStillRequiresData(t *testing.T) {
	var c client.Client
	resp := &client.Response{StatusCode: http.StatusOK, Body: []byte(`{"code":200}`)}
	if _, err := c.DecryptResponse(resp, 1); err == nil {
		t.Fatal("DecryptResponse accepted missing encrypted data")
	}
}
