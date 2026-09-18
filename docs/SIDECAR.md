# TLS Sidecar 出口(rustls 同源指纹)

## 解决什么问题

真实 Codex CLI 是 Rust 程序(reqwest 0.12 + rustls 0.23 + hyper h2)。本网关默认用 Go
标准库出站,TLS ClientHello(JA3/JA4)、HTTP/2 SETTINGS 序、头部写入顺序都是"Go 客户端"
特征——与 UA 声称的 Rust 客户端身份直接矛盾。uTLS Chrome 档是浏览器指纹,同样不是 CLI。

Sidecar 方案:一个小 Rust 进程与网关同容器运行,Go 把上游请求经**明文回环**交给它,
由它用与官方客户端同源的技术栈建立真实 TLS 连接。传输层指纹不再是"模拟",而是"就是"。

实测指纹(通过 tls.peet.ws):

| 出站方式 | JA4 | 说明 |
| --- | --- | --- |
| Go 标准库(原 standard 档) | `t13d1312h2_f57a46bbacb6_...` | 13 cipher / 12 ext,Go 特征 |
| **sidecar(rustls)** | `t13d1011h2_61a7ad8aa9b6_...` | 10 cipher / 11 ext,rustls 特征,与 codex CLI 同族 |

## 启用方式(Docker)

镜像已包含 sidecar 二进制,`docker-compose.local.yml` 等本地构建链路自动生效:

```bash
# .env
CODEX_TRANSPORT_MODE=sidecar
CODEX_SIDECAR_URL=http://127.0.0.1:9101
```

容器 entrypoint 检测到 `CODEX_SIDECAR_URL` 非空时,自动在后台拉起 sidecar 并监听对应
回环地址;主进程按上述两个变量把 Codex 上游流量切过去。

## 本地开发(Go 直跑)

```bash
# 方式一:本机有 Rust 工具链
cd sidecar && SIDECAR_ADDR=127.0.0.1:9101 cargo run --release

# 方式二:用 Docker 跑
docker build --target sidecar-builder -t codex-sidecar-build .
docker run -d --name codex-sidecar -p 127.0.0.1:9101:9101 \
  -e SIDECAR_ADDR=0.0.0.0:9101 --entrypoint /codex-egress-sidecar codex-sidecar-build

# 然后正常 go run .,并在环境里设置
# CODEX_TRANSPORT_MODE=sidecar CODEX_SIDECAR_URL=http://127.0.0.1:9101
```

## 验证

```bash
curl -s "http://127.0.0.1:9101/api/all" -H "x-codex2api-upstream-host: tls.peet.ws" | grep ja4
# 期望: t13d1011h2_...(rustls 族)
```

## 设计细节

- **出口代理链**:账号绑定的代理(HTTP/HTTPS/SOCKS5)经 base64url 控制头传给
  sidecar,由它负责拨号;每个代理一个独立 reqwest 客户端连接池。Resin 反代启用时
  整层覆盖逻辑不变(sidecar 直连 Resin)。
- **请求体**:有 content-length 且 ≤64MiB 的请求整段缓冲后**定长**转发(与真实客户端
  分帧一致),其余流式转发;响应(SSE)全程流式透传。
- **连接池**:Go 侧仍按 账号|代理|模式 隔离池键,代理语义与 standard 档一致。
- **失败语义**:sidecar 不可达/上游失败返回 502,网关按常规上游错误处理;sidecar
  缺失时启动日志会明确警告并回退 Go 标准传输(可用性优先,但日志可见)。

## 已知边界(截至当前版本)

- 仅覆盖 Codex **HTTP** 路径(`/responses` 等);WebSocket 中继(wsrelay)有独立拨号器,
  不经 sidecar。`CODEX_UPSTREAM_TRANSPORT=http`(默认)时无影响。
- token 刷新(`auth.openai.com`)与维护类请求走独立客户端,不经 sidecar。
- rustls 与官方 CLI 的 rustls 同版本族,但官方自定义 provider 若未来引入新扩展,
  需要跟进 rustls 版本。
