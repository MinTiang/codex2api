//! codex-egress-sidecar：本机 TLS 出口代理。
//!
//! 职责只有一件事：把网关经明文回环传来的上游请求，用与官方 Codex CLI 同源的
//! TLS 技术栈（reqwest 0.12 + rustls 0.23 + hyper h2）重新发往真实上游。
//! 传输层指纹（JA3/JA4、HTTP/2 SETTINGS 序、头部写入顺序）因此与真实客户端
//! 同源，而不是 Go 标准库或 uTLS 浏览器模拟的"近似值"。
//!
//! 协议（Go 网关 ↔ sidecar，仅回环）：
//!   - 请求 URL 的 host 部分是 sidecar 地址；真实上游主机放在
//!     `x-codex2api-upstream-host` 头，scheme 恒为 https。
//!   - 账号绑定的出口代理（http/https/socks5 URL，base64url）放在
//!     `x-codex2api-upstream-proxy` 头；缺失时直连。
//!   - 其余请求头原样透传（剥离逐跳头与控制头），请求体与响应体逐块流式转发，
//!     不缓冲、不改写，SSE 透传。
//!
//! 失败语义：上游不可达返回 502（带原因文本），由网关按常规上游错误处理。

use std::collections::HashMap;
use std::convert::Infallible;
use std::error::Error;
use std::net::SocketAddr;
use std::sync::{Mutex, OnceLock};

use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine;
use futures_util::StreamExt;
use http_body_util::combinators::BoxBody;
use http_body_util::{BodyExt, Full, StreamBody};
use hyper::body::{Bytes, Frame, Incoming};
use hyper::header::HeaderMap;
use hyper::server::conn::http1;
use hyper::service::service_fn;
use hyper::{Request, Response, StatusCode};
use hyper_util::rt::TokioIo;
use reqwest::{Client, Proxy};
use tokio::net::TcpListener;

const UPSTREAM_HOST_HEADER: &str = "x-codex2api-upstream-host";
const UPSTREAM_PROXY_HEADER: &str = "x-codex2api-upstream-proxy";

/// 请求体缓冲上限：有 content-length 且不超过该值时整段缓冲后转发（保证上游
/// 看到与真实客户端一致的定长请求），超过或无 content-length 时流式转发。
const MAX_BUFFERED_REQUEST_BYTES: usize = 64 * 1024 * 1024;

type ReqBody = BoxBody<Bytes, Box<dyn Error + Send + Sync>>;

fn full_body(text: String) -> ReqBody {
    BoxBody::new(Full::new(Bytes::from(text)).map_err(|err| match err {}))
}

fn is_hop_by_hop(name: &str) -> bool {
    matches!(
        name,
        "connection"
            | "keep-alive"
            | "proxy-authenticate"
            | "proxy-authorization"
            | "proxy-connection"
            | "te"
            | "trailer"
            | "transfer-encoding"
            | "upgrade"
            // 请求体定长转发由缓冲逻辑决定，不从原始头继承。
            | "content-length"
            // 网关 → sidecar 的控制头，绝不能上行。
            | "host"
    ) || name == UPSTREAM_HOST_HEADER
        || name == UPSTREAM_PROXY_HEADER
}

/// 按出口代理缓存的 reqwest 客户端。Client 是廉价 Arc 克隆；每个代理一个客户端，
/// 连接池天然按代理隔离，直连（空 key）共享一个池。
static PROXY_CLIENTS: OnceLock<Mutex<HashMap<String, Client>>> = OnceLock::new();

fn client_for(proxy_url: Option<&str>) -> Result<Client, Box<dyn Error + Send + Sync>> {
    let key = proxy_url.unwrap_or("").to_string();
    let cache = PROXY_CLIENTS.get_or_init(|| Mutex::new(HashMap::new()));
    if let Some(client) = cache.lock().expect("client cache poisoned").get(&key) {
        return Ok(client.clone());
    }
    let mut builder = Client::builder();
    if let Some(url) = proxy_url {
        if !url.is_empty() {
            builder = builder.proxy(Proxy::all(url)?);
        }
    }
    let client = builder.build()?;
    cache
        .lock()
        .expect("client cache poisoned")
        .insert(key, client.clone());
    Ok(client)
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn Error>> {
    let addr: SocketAddr = std::env::var("SIDECAR_ADDR")
        .unwrap_or_else(|_| "127.0.0.1:9101".to_string())
        .parse()?;
    let listener = TcpListener::bind(addr).await?;
    eprintln!("[codex-egress-sidecar] listening on {addr} (rustls egress)");
    loop {
        let (stream, _) = listener.accept().await?;
        let io = TokioIo::new(stream);
        tokio::task::spawn(async move {
            let service = service_fn(move |req: Request<Incoming>| async move { handle(req).await });
            if let Err(err) = http1::Builder::new().serve_connection(io, service).await {
                eprintln!("[codex-egress-sidecar] connection error: {err}");
            }
        });
    }
}

async fn handle(req: Request<Incoming>) -> Result<Response<ReqBody>, Infallible> {
    let proxy_header = req
        .headers()
        .get(UPSTREAM_PROXY_HEADER)
        .and_then(|value| value.to_str().ok())
        .map(|value| value.trim().to_string());
    let proxy_url = match proxy_header {
        Some(encoded) if !encoded.is_empty() => match URL_SAFE_NO_PAD.decode(encoded) {
            Ok(bytes) => match std::str::from_utf8(&bytes) {
                Ok(url) => Some(url.to_string()),
                Err(err) => return Ok(bad_gateway(format!("proxy url decode: {err}"))),
            },
            Err(err) => return Ok(bad_gateway(format!("proxy base64 decode: {err}"))),
        },
        _ => None,
    };
    let client = match client_for(proxy_url.as_deref()) {
        Ok(client) => client,
        Err(err) => return Ok(bad_gateway(format!("client build: {err}"))),
    };

    match forward(req, &client).await {
        Ok(resp) => Ok(resp),
        Err(err) => {
            eprintln!("[codex-egress-sidecar] upstream error: {err}");
            Ok(bad_gateway(format!("upstream error: {err}")))
        }
    }
}

fn bad_gateway(text: String) -> Response<ReqBody> {
    let mut response = Response::new(full_body(format!("codex-egress-sidecar: {text}")));
    *response.status_mut() = StatusCode::BAD_GATEWAY;
    response
}

async fn forward(req: Request<Incoming>, client: &Client) -> Result<Response<ReqBody>, Box<dyn Error + Send + Sync>> {
    let upstream_host = req
        .headers()
        .get(UPSTREAM_HOST_HEADER)
        .and_then(|value| value.to_str().ok())
        .unwrap_or("")
        .trim()
        .to_string();
    if upstream_host.is_empty() {
        return Err("missing x-codex2api-upstream-host header".into());
    }
    let path_and_query = req
        .uri()
        .path_and_query()
        .map(|pq| pq.as_str().to_string())
        .unwrap_or_else(|| "/".to_string());
    let url = format!("https://{upstream_host}{path_and_query}");

    let mut headers = HeaderMap::new();
    for (name, value) in req.headers() {
        if is_hop_by_hop(name.as_str()) {
            continue;
        }
        headers.append(name.clone(), value.clone());
    }

    let mut builder = client.request(req.method().clone(), &url);
    builder = builder.headers(headers);

    // 有 content-length 且不超限的请求整段缓冲后定长转发；否则流式转发。
    let content_length = req
        .headers()
        .get("content-length")
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.parse::<usize>().ok());
    let request = if matches!(content_length, Some(len) if len <= MAX_BUFFERED_REQUEST_BYTES) {
        let bytes = req.into_body().collect().await?.to_bytes();
        builder.body(reqwest::Body::from(bytes))
    } else {
        builder.body(reqwest::Body::wrap_stream(req.into_body().into_data_stream()))
    };

    let upstream_response = request.send().await?;
    let status = upstream_response.status();
    let mut response_headers = HeaderMap::new();
    for (name, value) in upstream_response.headers() {
        if is_hop_by_hop(name.as_str()) {
            continue;
        }
        response_headers.append(name.clone(), value.clone());
    }

    let mut response = Response::new(BoxBody::new(StreamBody::new(
        upstream_response.bytes_stream().map(|result| {
            result
                .map(Frame::data)
                .map_err(|err| -> Box<dyn Error + Send + Sync> { Box::new(err) })
        }),
    )));
    *response.status_mut() = status;
    *response.headers_mut() = response_headers;
    Ok(response)
}
