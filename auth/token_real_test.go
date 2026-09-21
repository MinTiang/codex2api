package auth

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestRefreshRealPlatformToken 用真实 platform token 验证续期能力。
// 需要 /tmp/real_rt.txt + /tmp/real_idt.txt（由 turb 测试产出），缺失则跳过。
func TestRefreshRealPlatformToken(t *testing.T) {
	// 需要真实 token 才跑(turb 测试产出); 环境变量守门避免 CI 误跑
	if os.Getenv("RUN_REAL_TOKEN_TEST") != "1" {
		t.Skip("未设置 RUN_REAL_TOKEN_TEST=1，跳过真实 token 测试")
	}
	rtBytes, err := os.ReadFile("/tmp/real_rt.txt")
	if err != nil {
		t.Skip("无真实 RT，跳过")
	}
	rt := strings.TrimSpace(string(rtBytes))
	if rt == "" {
		t.Skip("RT 为空，跳过")
	}
	idtBytes, _ := os.ReadFile("/tmp/real_idt.txt")
	idt := strings.TrimSpace(string(idtBytes))

	// 1) 验证探测：id_token 应能识别出 platform client
	detected := DetectClientIDFromToken(idt, rt)
	t.Logf("探测到的 client_id: %q", detected)
	if detected != PlatformClientID {
		t.Fatalf("应探测到 platform client, got=%q", detected)
	}

	// 2) 端到端刷新（走本机 Clash 代理）
	td, info, err := RefreshAccessTokenWithClientID(context.Background(), rt, idt, "", "socks5h://127.0.0.1:7897")
	if err != nil {
		t.Fatalf("刷新失败: %v", err)
	}
	t.Logf("✅ 刷新成功: AT=%d 字符, 新 RT=%v, email=%s", len(td.AccessToken), td.RefreshToken != "", info.Email)
	if len(td.AccessToken) < 500 {
		t.Fatalf("AT 异常短: %d", len(td.AccessToken))
	}
}
