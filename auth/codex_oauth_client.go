package auth

import "strings"

// CodexOAuthClientIDCredentialKey 是「签发该账号 token 的 OAuth client」在
// 账号 credentials 里的存储键。
//
// 为什么必须记下来：OpenAI 的 refresh_token 是 client 绑定的，用别的 client
// 去刷新会返回 401 invalid_client。当前有两种来源：
//
//	接码授权（Codex CLI）        app_EMoamEEZ73f0CkXaXp7hrann
//	平台免接码授权（官网 platform） app_2SKx67EdpoN0G6j64rFvigXD
//
// 平台签发的 RT 只能用它自己的 client 刷新（实测 2026-09-22）。不记这一项时，
// 刷新只能靠 id_token 的 JWT 声明反推；平台号一旦 id_token 缺失或过期就会
// 退回 CLI client 从而续期失败。导入时显式写入本键，id_token 只作为兜底。
const CodexOAuthClientIDCredentialKey = "oauth_client_id"

// 与 auth/token.go 中的常量保持一致，便于导入侧直接引用。
const (
	// CodexCLIOAuthClientID 是 Codex CLI 官方客户端（接码授权流程）。
	CodexCLIOAuthClientID = ClientID
	// CodexPlatformOAuthClientID 是 platform.openai.com 官网客户端（免接码授权流程）。
	CodexPlatformOAuthClientID = PlatformClientID
)

// NormalizeCodexOAuthClientID 归一化待写入的 client_id。
// 只接受已知的两个官方 client，其余（含空值）返回空串，表示不写入该键、
// 刷新时按 id_token 反推再回退 CLI client。避免把脏值写进账号导致刷新全挂。
func NormalizeCodexOAuthClientID(value string) string {
	switch strings.TrimSpace(value) {
	case CodexCLIOAuthClientID:
		return CodexCLIOAuthClientID
	case CodexPlatformOAuthClientID:
		return CodexPlatformOAuthClientID
	default:
		return ""
	}
}
