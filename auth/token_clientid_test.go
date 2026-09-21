package auth

import "testing"

func TestDetectClientIDFromToken(t *testing.T) {
	// 构造 platform token 的 JWT(仅 payload 段参与解析)
	platformTok := "x.eyJhdWQiOlsiaHR0cHM6Ly9hcGkub3BlbmFpLmNvbS92MSJdLCJjbGllbnRfaWQiOiJhcHBfMlNLeDY3RWRwb04wRzZqNjRyRnZpZ1hEIn0.sig"
	if got := DetectClientIDFromToken(platformTok); got != PlatformClientID {
		t.Fatalf("platform 探测失败: got=%q want=%q", got, PlatformClientID)
	}
	// aud 为字符串形式
	audStr := "x.eyJhdWQiOiJhcHBfMlNLeDY3RWRwb04wRzZqNjRyRnZpZ1hEIn0.sig"
	if got := DetectClientIDFromToken(audStr); got != PlatformClientID {
		t.Fatalf("aud 字符串探测失败: got=%q", got)
	}
	// 非 JWT / 空值 → 空串
	for _, bad := range []string{"", "not-a-jwt", "a.b"} {
		if got := DetectClientIDFromToken(bad); got != "" {
			t.Fatalf("非 JWT 应返回空串: input=%q got=%q", bad, got)
		}
	}
}

func TestResolveRefreshClientID(t *testing.T) {
	platformTok := "x.eyJjbGllbnRfaWQiOiJhcHBfMlNLeDY3RWRwb04wRzZqNjRyRnZpZ1hEIn0.sig"
	// 1) 显式 override 优先
	if got := ResolveRefreshClientID("rt", platformTok, "app_custom"); got != "app_custom" {
		t.Fatalf("override 未生效: %q", got)
	}
	// 2) 从 token 探测
	if got := ResolveRefreshClientID("rt", platformTok, ""); got != PlatformClientID {
		t.Fatalf("探测未生效: %q", got)
	}
	// 3) 回退默认 CLI client
	if got := ResolveRefreshClientID("opaque-rt", "", ""); got != ClientID {
		t.Fatalf("回退失败: %q", got)
	}
}
