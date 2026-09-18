package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestCodexSidecarTransportRewritesUpstream 验证 sidecar transport 的改写契约：
// https 上游请求改写到回环 sidecar，真实主机与出口代理走控制头，路径与查询原样保留。
func TestCodexSidecarTransportRewritesUpstream(t *testing.T) {
	var gotPath, gotHost, gotUpstream, gotProxy string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		gotHost = r.Host
		gotUpstream = r.Header.Get(CodexSidecarUpstreamHostHeader)
		gotProxy = r.Header.Get(CodexSidecarUpstreamProxyHeader)
		body, _ := io.ReadAll(r.Body)
		if string(body) != "request-body" {
			t.Errorf("sidecar received body = %q, want %q", string(body), "request-body")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: ok\n\n"))
	}))
	defer upstream.Close()

	transport := &codexSidecarTransport{
		base:     upstream.Client().Transport.(*http.Transport),
		addr:     strings.TrimPrefix(upstream.URL, "http://"),
		proxyB64: "c29ja3M1Oi8vdXNlcjpwYXNzQDE5Mi4wLjIuMToxMDgw",
	}
	req, err := http.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses?x=1", strings.NewReader("request-body"))
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer token")
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if gotPath != "/backend-api/codex/responses?x=1" {
		t.Fatalf("sidecar got path %q, want rewritten upstream path preserved", gotPath)
	}
	if gotUpstream != "chatgpt.com" {
		t.Fatalf("upstream host header = %q, want chatgpt.com", gotUpstream)
	}
	if gotProxy != transport.proxyB64 {
		t.Fatalf("upstream proxy header = %q, want %q", gotProxy, transport.proxyB64)
	}
	if gotHost != transport.addr {
		t.Fatalf("request Host = %q, want sidecar addr %q", gotHost, transport.addr)
	}
}

// TestCodexSidecarAddrFromEnv 校验地址解析：支持 http(s) 前缀，拒绝非法形态。
func TestCodexSidecarAddrFromEnv(t *testing.T) {
	t.Setenv("CODEX_SIDECAR_URL", "http://127.0.0.1:9101")
	codexSidecarAddrOnce = *new(sync.Once)
	codexSidecarAddr = ""
	if got := codexSidecarAddrFromEnv(); got != "127.0.0.1:9101" {
		t.Fatalf("addr = %q, want 127.0.0.1:9101", got)
	}

	t.Setenv("CODEX_SIDECAR_URL", "not-a-host")
	codexSidecarAddrOnce = *new(sync.Once)
	codexSidecarAddr = ""
	if got := codexSidecarAddrFromEnv(); got != "" {
		t.Fatalf("invalid addr = %q, want empty", got)
	}

	// 还原，避免影响其它测试。
	codexSidecarAddrOnce = *new(sync.Once)
	codexSidecarAddr = ""
}
