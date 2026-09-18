#!/bin/sh
# 容器入口：按需拉起 TLS sidecar，再启动主进程。
# CODEX_SIDECAR_URL 非空时，从 URL 提取 host:port 作为 sidecar 监听地址在后台启动；
# 主进程侧 CODEX_TRANSPORT_MODE=sidecar + CODEX_SIDECAR_URL 才会真正走 sidecar 出口。
set -e

if [ -x /usr/local/bin/codex-egress-sidecar ] && [ -n "$CODEX_SIDECAR_URL" ]; then
    SIDECAR_ADDR="$(printf '%s' "$CODEX_SIDECAR_URL" | sed -E 's#^https?://##; s#/$##')"
    if [ -n "$SIDECAR_ADDR" ]; then
        SIDECAR_ADDR="$SIDECAR_ADDR" /usr/local/bin/codex-egress-sidecar &
    fi
fi

exec /usr/local/bin/codex2api "$@"
