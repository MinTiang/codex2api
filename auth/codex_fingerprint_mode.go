package auth

import (
	"os"
	"strings"
	"sync"
	"sync/atomic"
)

// Codex 设备指纹收敛模式。多人共享同一个 Codex OAuth 账号时，每个下游用户的
// Codex 客户端携带各自的 installation_id / session_id / thread_id，上游据此
// 判定这一个账号背后有多少台设备、多少个会话。收敛模式把这些标识改写成账号级
// 恒定值，让上游看到的设备/会话数收敛到接近单人使用的形态。
//
// 只影响出站请求里的 x-codex-turn-metadata 头和请求体 client_metadata；
// 出站 Session_id 头由 resolveUpstreamSessionID 独立决定，收敛不参与，
// 因此 prompt cache 隔离行为和 isolate_requests_by_default 设置不受影响。
const (
	// CodexFingerprintModeOff 不做任何收敛，客户端标识原样透传。
	CodexFingerprintModeOff = "off"
	// CodexFingerprintModeDevice 仅把 installation_id 收敛为账号级恒定值。
	// 上游看到 1 台设备 + 每个下游用户各自的会话。
	CodexFingerprintModeDevice = "device"
	// CodexFingerprintModeSession 收敛 installation_id + session_id，并按客户端
	// 原始会话标识确定性派生 thread_id：每个真实 Codex 会话得到一个独立线程。
	// 上游看到 1 台设备 + 1 会话 + N 线程，接近单人开多窗口/spawn 子代理的形态。
	CodexFingerprintModeSession = "session"
	// CodexFingerprintModeFull 令 thread_id 等于 session_id，上游看到
	// 1 台设备 + 1 会话 + 1 线程。最激进，也最不像真实客户端的并发形态。
	CodexFingerprintModeFull = "full"
)

// CodexFingerprintModeCredentialKey 是该模式在账号 credentials 中的存储键。
const CodexFingerprintModeCredentialKey = "codex_fingerprint_mode"

// 部署级默认档位。未显式配置档位的账号（含全部存量账号）按它生效。
//
// 上游对网关流量的识别早已不限于单账号特征：同一开源项目的所有部署若共享
// 同一套派生常数与默认行为，会在上游视角聚类成同一个「长得像官方客户端但
// 不是官方客户端」的群体，一锅端时无人幸免。收敛（session）而非透传（off）
// 作为默认，是为了让单个账号在上游呈现单人形态；部署可用系统设置
// codex_fingerprint_default_mode 或环境变量 CODEX_FINGERPRINT_DEFAULT_MODE
// 调整（含显式回退 off）。
//
// 优先级：账号显式凭据 > 环境变量 > 系统设置 > 内置 session。
var (
	codexDefaultModeEnvOnce    sync.Once
	codexDefaultModeEnvValue   atomic.Value // string；环境变量归一结果，空串表示未设置或非法
	codexDefaultModeStoreValue atomic.Value // string；系统设置归一结果，空串表示未设置
)

// normalizeStrictCodexFingerprintMode 只认四个显式档位；空串与非法值返回空串，
// 表示「这一层没有给出有效值」，由下一层兜底。
func normalizeStrictCodexFingerprintMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case CodexFingerprintModeOff:
		return CodexFingerprintModeOff
	case CodexFingerprintModeDevice:
		return CodexFingerprintModeDevice
	case CodexFingerprintModeSession:
		return CodexFingerprintModeSession
	case CodexFingerprintModeFull:
		return CodexFingerprintModeFull
	default:
		return ""
	}
}

// SetDeploymentCodexFingerprintDefaultMode 由 Store 在加载或更新系统设置时调用，
// 把 codex_fingerprint_default_mode 同步为账号生效的部署级默认档。
func SetDeploymentCodexFingerprintDefaultMode(mode string) {
	codexDefaultModeStoreValue.Store(normalizeStrictCodexFingerprintMode(mode))
}

// codexDefaultModeFromEnv 读取进程启动时的环境变量覆盖（只解析一次）。
func codexDefaultModeFromEnv() string {
	codexDefaultModeEnvOnce.Do(func() {
		codexDefaultModeEnvValue.Store(normalizeStrictCodexFingerprintMode(os.Getenv("CODEX_FINGERPRINT_DEFAULT_MODE")))
	})
	value, _ := codexDefaultModeEnvValue.Load().(string)
	return value
}

// DefaultCodexFingerprintMode 返回未显式配置账号的生效档位：
// 环境变量 > 系统设置 > 内置 session。
func DefaultCodexFingerprintMode() string {
	if value := codexDefaultModeFromEnv(); value != "" {
		return value
	}
	if value, ok := codexDefaultModeStoreValue.Load().(string); ok && value != "" {
		return value
	}
	return CodexFingerprintModeSession
}

// NormalizeCodexFingerprintMode 归一化模式取值。空值落到部署默认档
// （DefaultCodexFingerprintMode，内置 session），保证未配置的账号开箱即收敛；
// 非法值仍落到 off，显式写错不等于同意收敛。
func NormalizeCodexFingerprintMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case CodexFingerprintModeDevice:
		return CodexFingerprintModeDevice
	case CodexFingerprintModeSession:
		return CodexFingerprintModeSession
	case CodexFingerprintModeFull:
		return CodexFingerprintModeFull
	case CodexFingerprintModeOff:
		return CodexFingerprintModeOff
	case "":
		return DefaultCodexFingerprintMode()
	default:
		return CodexFingerprintModeOff
	}
}

// IsValidCodexFingerprintMode 报告取值是否为四个已知档位之一。
func IsValidCodexFingerprintMode(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case CodexFingerprintModeOff, CodexFingerprintModeDevice, CodexFingerprintModeSession, CodexFingerprintModeFull:
		return true
	default:
		return false
	}
}

// EffectiveCodexFingerprintMode 返回账号生效的收敛模式。
// 中转型账号（OpenAI Responses / Grok）不走 Codex 官方出站路径，恒为 off。
func (a *Account) EffectiveCodexFingerprintMode() string {
	if a == nil {
		return CodexFingerprintModeOff
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isRelayStyleLocked() {
		return CodexFingerprintModeOff
	}
	return NormalizeCodexFingerprintMode(a.CodexFingerprintMode)
}
