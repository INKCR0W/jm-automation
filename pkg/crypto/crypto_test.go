package crypto

import (
	"crypto/aes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestDecodeBase64ECBPKCS7(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef"

	tests := []struct {
		name      string
		data      string
		want      string
		wantError string
	}{
		{
			name: "valid padding",
			data: base64.StdEncoding.EncodeToString(encryptECBForTest(t, []byte("hello"+strings.Repeat(string([]byte{11}), 11)), []byte(key))),
			want: "hello",
		},
		{
			name:      "invalid base64",
			data:      "%%%not-base64%%%",
			wantError: "base64 decode failed",
		},
		{
			name:      "ciphertext is not block aligned",
			data:      base64.StdEncoding.EncodeToString([]byte("short")),
			wantError: "ciphertext is not a multiple of the block size",
		},
		{
			name:      "invalid pkcs7 padding",
			data:      base64.StdEncoding.EncodeToString(encryptECBForTest(t, []byte("0123456789ABCDEF"), []byte(key))),
			wantError: "pkcs7 padding",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeBase64ECBPKCS7(tt.data, key)
			if tt.wantError != "" {
				if err == nil {
					t.Fatal("DecodeBase64ECBPKCS7 returned nil error")
				}
				if !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("error = %q, want substring %q", err.Error(), tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeBase64ECBPKCS7 returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("plaintext = %q, want %q", got, tt.want)
			}
		})
	}
}

func encryptECBForTest(t *testing.T, plaintext, key []byte) []byte {
	t.Helper()

	if len(plaintext)%aes.BlockSize != 0 {
		t.Fatalf("plaintext length %d is not block aligned", len(plaintext))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("new cipher failed: %v", err)
	}

	ciphertext := make([]byte, len(plaintext))
	for i := 0; i < len(plaintext); i += aes.BlockSize {
		block.Encrypt(ciphertext[i:i+aes.BlockSize], plaintext[i:i+aes.BlockSize])
	}
	return ciphertext
}
