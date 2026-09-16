package crypto

import "testing"

func TestTokenAndTokenParam(t *testing.T) {
	token, tokenparam := TokenAndTokenParam(1700566805000, "2.0.20")

	if want := "1700566805000,2.0.20"; tokenparam != want {
		t.Fatalf("tokenparam = %q, want %q", tokenparam, want)
	}
	// 业务接口是 md5(ts + version)
	if want := MD5Hex("17005668050002.0.20"); token != want {
		t.Fatalf("token = %q, want %q", token, want)
	}
}

func TestTokenAndTokenParamWithSeed(t *testing.T) {
	token, tokenparam := TokenAndTokenParamWithSeed(1700566805, "2.0.20", AppDataSecret)

	if want := "1700566805,2.0.20"; tokenparam != want {
		t.Fatalf("tokenparam = %q, want %q", tokenparam, want)
	}
	// /setting 是 md5(ts + AppDataSecret)，跟业务接口不一样
	if want := MD5Hex("1700566805" + AppDataSecret); token != want {
		t.Fatalf("token = %q, want %q", token, want)
	}
	if token == MD5Hex("17005668052.0.20") {
		t.Fatal("seeded token must not fall back to the version-based token")
	}
}
