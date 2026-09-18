package proxy

import (
	"crypto/tls"
	"encoding/base64"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Codex sidecar 出口（TLS 指纹同源层）。
//
// CODEX_TRANSPORT_MODE=sidecar 时，Codex 上游请求改经本机 sidecar 进程转发：
// Go 只把请求明文送到回环上的 sidecar，由它用与官方 Codex CLI 同源的技术栈
// （reqwest 0.12 + rustls 0.23 + hyper h2）建立真实 TLS 连接。Go 标准库的
// TLS ClientHello、HTTP/2 SETTINGS 序与头部写入顺序从此不出现在上游视角里。
//
// 协议约定见 sidecar/src/main.rs：
//   - 真实上游主机放 X-Codex2api-Upstream-Host 头，scheme 恒为 https；
//   - 账号出口代理（base64url）放 X-Codex2api-Upstream-Proxy 头，由 sidecar
//     负责拨号——连接池仍按 账号|代理|模式 隔离，语义不变；
//   - 其余头、请求体、SSE 响应体全部透传。
//
// WS 路径不走 sidecar（wsrelay 有独立拨号器，本模式只覆盖 HTTP /responses）。

const (
	CodexSidecarUpstreamHostHeader  = "X-Codex2api-Upstream-Host"
	CodexSidecarUpstreamProxyHeader = "X-Codex2api-Upstream-Proxy"

	codexTransportModeSidecar = "sidecar"
)

var (
	codexSidecarAddrOnce sync.Once
	codexSidecarAddr     string
	codexSidecarWarnOnce sync.Once
)

// codexSidecarAddrFromEnv 读取 sidecar 地址（如 http://127.0.0.1:9101）。
// 未配置返回空串。
func codexSidecarAddrFromEnv() string {
	codexSidecarAddrOnce.Do(func() {
		addr := strings.TrimRight(strings.TrimSpace(os.Getenv("CODEX_SIDECAR_URL")), "/")
		addr = strings.TrimPrefix(strings.TrimPrefix(addr, "https://"), "http://")
		if addr == "" {
			return
		}
		if _, _, err := net.SplitHostPort(addr); err != nil {
			log.Printf("[CodexSidecar] CODEX_SIDECAR_URL=%q 不是合法的 host:port，sidecar 模式不可用", addr)
			return
		}
		codexSidecarAddr = addr
	})
	return codexSidecarAddr
}

func warnCodexSidecarMissing() {
	codexSidecarWarnOnce.Do(func() {
		log.Printf("[CodexSidecar] CODEX_TRANSPORT_MODE=sidecar 但未配置 CODEX_SIDECAR_URL，回退 Go 标准传输（TLS 指纹退回 Go 默认，请部署 sidecar 或改回 standard/utls_chrome）")
	})
}

// codexSidecarTransport 把 https 上游请求改写到回环 sidecar，其余原样透传。
type codexSidecarTransport struct {
	base     *http.Transport
	addr     string
	proxyB64 string
}

func newCodexSidecarTransport(proxyURL string) http.RoundTripper {
	addr := codexSidecarAddrFromEnv()
	if addr == "" {
		warnCodexSidecarMissing()
		return newCodexStandardTransport(proxyURL)
	}
	transport := &http.Transport{
		// Go ↔ sidecar 是本机明文 http/1.1，不涉及 TLS；h2 由 sidecar 到上游那段负责。
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 5 * time.Minute,
		ForceAttemptHTTP2:     false,
		TLSClientConfig:       &tls.Config{},
	}
	encoded := ""
	if trimmed := strings.TrimSpace(proxyURL); trimmed != "" {
		encoded = base64.RawURLEncoding.EncodeToString([]byte(trimmed))
	}
	return &codexSidecarTransport{base: transport, addr: addr, proxyB64: encoded}
}

func (t *codexSidecarTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme == "https" {
		rewritten := req.Clone(req.Context())
		upstreamHost := rewritten.URL.Host
		rewritten.URL.Scheme = "http"
		rewritten.URL.Host = t.addr
		rewritten.Host = t.addr
		rewritten.Header = rewritten.Header.Clone()
		rewritten.Header.Set(CodexSidecarUpstreamHostHeader, upstreamHost)
		if t.proxyB64 != "" {
			rewritten.Header.Set(CodexSidecarUpstreamProxyHeader, t.proxyB64)
		} else {
			rewritten.Header.Del(CodexSidecarUpstreamProxyHeader)
		}
		req = rewritten
	}
	return t.base.RoundTrip(req)
}

func (t *codexSidecarTransport) CloseIdleConnections() {
	t.base.CloseIdleConnections()
}
