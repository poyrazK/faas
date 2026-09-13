#!/usr/bin/env bash
# Prove the public hostname works through Cloudflare while a direct connection
# to the origin cannot reach Caddy. Read-only; no DNS or firewall mutations.
set -euo pipefail

host="${FAAS_PUBLIC_HOST:-api.gregale.dev}"
origin_ip="${FAAS_ORIGIN_IP:-}"
path="${FAAS_PUBLIC_HEALTH_PATH:-/healthz}"
timeout="${FAAS_ORIGIN_PROBE_TIMEOUT_SECONDS:-5}"

[[ -n "$origin_ip" ]] || {
  echo "FAAS_ORIGIN_IP is required" >&2
  exit 2
}

public_headers="$(mktemp)"
direct_headers="$(mktemp)"
trap 'rm -f "$public_headers" "$direct_headers"' EXIT

curl --fail --silent --show-error --max-time "$timeout" \
  --dump-header "$public_headers" --output /dev/null "https://${host}${path}"
grep -Eiq '^server:[[:space:]]*cloudflare' "$public_headers" || {
  echo "public request did not carry Cloudflare's server header" >&2
  exit 1
}

if curl --insecure --silent --show-error --max-time "$timeout" \
    --resolve "${host}:443:${origin_ip}" --dump-header "$direct_headers" \
    --output /dev/null "https://${host}${path}"; then
  status="$(awk 'toupper($1) ~ /^HTTP\// { code=$2 } END { print code }' "$direct_headers")"
  echo "direct origin bypass unexpectedly returned HTTP ${status:-unknown}" >&2
  exit 1
fi

echo "cloudflare-origin-smoke: PASS public=cloudflare direct_origin=blocked"
