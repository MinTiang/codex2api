package auth

import (
	"sync"
	"testing"
)

func TestNormalizeCodexFingerprintMode(t *testing.T) {
	// 空值落到部署默认档（内置 session；环境变量与系统设置可覆盖）。
	cases := map[string]string{
		"":          DefaultCodexFingerprintMode(),
		"off":       CodexFingerprintModeOff,
		"unknown":   CodexFingerprintModeOff,
		"DEVICE":    CodexFingerprintModeDevice,
		" session ": CodexFingerprintModeSession,
		"Full":      CodexFingerprintModeFull,
	}
	for input, want := range cases {
		if got := NormalizeCodexFingerprintMode(input); got != want {
			t.Errorf("NormalizeCodexFingerprintMode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestIsValidCodexFingerprintMode(t *testing.T) {
	for _, value := range []string{CodexFingerprintModeOff, CodexFingerprintModeDevice, CodexFingerprintModeSession, CodexFingerprintModeFull, " FULL "} {
		if !IsValidCodexFingerprintMode(value) {
			t.Errorf("IsValidCodexFingerprintMode(%q) = false, want true", value)
		}
	}
	for _, value := range []string{"", "converge", "device-only"} {
		if IsValidCodexFingerprintMode(value) {
			t.Errorf("IsValidCodexFingerprintMode(%q) = true, want false", value)
		}
	}
}

func TestDefaultCodexFingerprintModeBuiltin(t *testing.T) {
	// 未设置环境变量与系统设置时，内置默认是 session（收敛开箱即用）。
	SetDeploymentCodexFingerprintDefaultMode("")
	t.Setenv("CODEX_FINGERPRINT_DEFAULT_MODE", "")
	codexDefaultModeEnvOnce = *new(sync.Once)
	if got := DefaultCodexFingerprintMode(); got != CodexFingerprintModeSession {
		t.Errorf("builtin default mode = %q, want %q", got, CodexFingerprintModeSession)
	}

	// 环境变量是部署级紧急开关：显式 off 覆盖一切，回到旧行为。
	t.Setenv("CODEX_FINGERPRINT_DEFAULT_MODE", "off")
	codexDefaultModeEnvOnce = *new(sync.Once)
	if got := DefaultCodexFingerprintMode(); got != CodexFingerprintModeOff {
		t.Errorf("env override mode = %q, want %q", got, CodexFingerprintModeOff)
	}

	// 环境变量非法时忽略，继续走系统设置层。
	t.Setenv("CODEX_FINGERPRINT_DEFAULT_MODE", "converge")
	codexDefaultModeEnvOnce = *new(sync.Once)
	SetDeploymentCodexFingerprintDefaultMode("device")
	if got := DefaultCodexFingerprintMode(); got != CodexFingerprintModeDevice {
		t.Errorf("store fallback mode = %q, want %q", got, CodexFingerprintModeDevice)
	}

	// 还原，避免影响其它测试。
	t.Setenv("CODEX_FINGERPRINT_DEFAULT_MODE", "")
	codexDefaultModeEnvOnce = *new(sync.Once)
	SetDeploymentCodexFingerprintDefaultMode("")
}

func TestEffectiveCodexFingerprintMode(t *testing.T) {
	if got := (*Account)(nil).EffectiveCodexFingerprintMode(); got != CodexFingerprintModeOff {
		t.Errorf("nil account mode = %q, want %q", got, CodexFingerprintModeOff)
	}

	// 未配置的账号按部署默认档生效（内置 session），升级即开收敛。
	if got := (&Account{DBID: 1}).EffectiveCodexFingerprintMode(); got != DefaultCodexFingerprintMode() {
		t.Errorf("unconfigured account mode = %q, want %q", got, DefaultCodexFingerprintMode())
	}

	codex := &Account{DBID: 1, CodexFingerprintMode: CodexFingerprintModeSession}
	if got := codex.EffectiveCodexFingerprintMode(); got != CodexFingerprintModeSession {
		t.Errorf("codex account mode = %q, want %q", got, CodexFingerprintModeSession)
	}

	// 显式 off 必须压过部署默认档。
	optOut := &Account{DBID: 1, CodexFingerprintMode: CodexFingerprintModeOff}
	if got := optOut.EffectiveCodexFingerprintMode(); got != CodexFingerprintModeOff {
		t.Errorf("explicit off account mode = %q, want %q", got, CodexFingerprintModeOff)
	}

	relay := &Account{
		DBID:                 1,
		UpstreamType:         UpstreamOpenAIResponses,
		BaseURL:              "https://relay.example.com",
		APIKey:               "sk-relay",
		CodexFingerprintMode: CodexFingerprintModeSession,
	}
	if got := relay.EffectiveCodexFingerprintMode(); got != CodexFingerprintModeOff {
		t.Errorf("relay account mode = %q, want %q (relays do not use the Codex outbound path)", got, CodexFingerprintModeOff)
	}

	// Grok 账号有独立的上游执行器，Codex 指纹收敛对它无意义，即使凭据里配了档位也必须为 off。
	grokAPIKey := &Account{
		DBID:                 1,
		UpstreamType:         UpstreamGrok,
		APIKey:               "xai-key",
		CodexFingerprintMode: CodexFingerprintModeFull,
	}
	if got := grokAPIKey.EffectiveCodexFingerprintMode(); got != CodexFingerprintModeOff {
		t.Errorf("grok api-key account mode = %q, want %q", got, CodexFingerprintModeOff)
	}
	grokOAuth := &Account{
		DBID:                 1,
		UpstreamType:         UpstreamGrok,
		RefreshToken:         "grok-rt",
		CodexFingerprintMode: CodexFingerprintModeSession,
	}
	if got := grokOAuth.EffectiveCodexFingerprintMode(); got != CodexFingerprintModeOff {
		t.Errorf("grok oauth account mode = %q, want %q", got, CodexFingerprintModeOff)
	}
}
