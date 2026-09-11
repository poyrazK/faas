#!/usr/bin/env bash
# Validate the operator-facing HTTPS endpoint, including the Caddy edge.
# This intentionally runs against the public URL rather than loopback so a
# healthy gatewayd-public process cannot mask a broken TLS/reverse-proxy path.
set -euo pipefail

endpoint="${PUBLIC_ENDPOINT_URL:-}"
http_endpoint="${PUBLIC_HTTP_URL:-}"
probe_path="${PUBLIC_ENDPOINT_PATH:-/status}"
min_hsts_age="${PUBLIC_MIN_HSTS_MAX_AGE:-31536000}"

if [[ -z "$endpoint" ]]; then
  echo "public-endpoint-check: PUBLIC_ENDPOINT_URL is required" >&2
  exit 2
fi
case "$endpoint" in
  https://*) ;;
  *)
    echo "public-endpoint-check: endpoint must use https://" >&2
    exit 2
    ;;
esac

case "$probe_path" in
  /*) ;;
  *)
    echo "public-endpoint-check: PUBLIC_ENDPOINT_PATH must start with /" >&2
    exit 2
    ;;
esac

if [[ "$endpoint" == */ ]]; then
  endpoint="${endpoint%/}"
fi
url="${endpoint}${probe_path}"
headers="$(mktemp)"
trap 'rm -f "$headers"' EXIT

status="$(curl --silent --show-error --location --max-time 20 \
  --proto '=https' --tlsv1.2 \
  --dump-header "$headers" --output /dev/null --write-out '%{http_code}' \
  "$url")"
case "$status" in
  2[0-9][0-9]) ;;
  *)
    echo "public-endpoint-check: ${url} returned HTTP ${status}; want 2xx" >&2
    exit 1
    ;;
esac

# Customer app endpoints are API surfaces. Exercise a common non-browser user
# agent so a zone-level Browser Integrity Check regression cannot pass the
# ordinary curl probe while rejecting standard library clients.
api_client_status="$(curl --silent --show-error --location --max-time 20 \
  --proto '=https' --tlsv1.2 --user-agent 'Python-urllib/3.13' \
  --output /dev/null --write-out '%{http_code}' "$url")"
case "$api_client_status" in
  2[0-9][0-9]) ;;
  *)
    echo "public-endpoint-check: ${url} returned HTTP ${api_client_status} to Python-urllib user agent; want 2xx" >&2
    exit 1
    ;;
esac

hsts="$(awk 'tolower($0) ~ /^strict-transport-security:/ { sub(/^[^:]*:[[:space:]]*/, ""); print; exit }' "$headers" | tr -d '\r')"
if [[ -z "$hsts" ]]; then
  echo "public-endpoint-check: missing Strict-Transport-Security header" >&2
  exit 1
fi
hsts_age="$(printf '%s\n' "$hsts" | sed -nE 's/.*max-age=([0-9]+).*/\1/p')"
if [[ -z "$hsts_age" || "$hsts_age" -lt "$min_hsts_age" ]]; then
  echo "public-endpoint-check: HSTS max-age ${hsts_age:-missing}; want >= ${min_hsts_age}" >&2
  exit 1
fi

if [[ -n "$http_endpoint" ]]; then
  case "$http_endpoint" in
    http://*) ;;
    *)
      echo "public-endpoint-check: PUBLIC_HTTP_URL must use http://" >&2
      exit 2
      ;;
  esac
  http_headers="$(mktemp)"
  trap 'rm -f "$headers" "$http_headers"' EXIT
  redirect_status="$(curl --silent --show-error --max-time 20 --max-redirs 0 \
    --dump-header "$http_headers" --output /dev/null --write-out '%{http_code}' \
    "$http_endpoint${probe_path}")" || true
  case "$redirect_status" in
    301|302|303|307|308) ;;
    *)
      echo "public-endpoint-check: HTTP endpoint returned ${redirect_status}; want redirect to HTTPS" >&2
      exit 1
      ;;
  esac
  location="$(awk 'tolower($0) ~ /^location:/ { sub(/^[^:]*:[[:space:]]*/, ""); print; exit }' "$http_headers" | tr -d '\r')"
  case "$location" in
    https://*) ;;
    *)
      echo "public-endpoint-check: HTTP redirect Location is not HTTPS" >&2
      exit 1
      ;;
  esac
fi

echo "public-endpoint-check: OK endpoint=${endpoint} status=${status} hsts_max_age=${hsts_age}"
echo "public-endpoint-check: OK api_client_user_agent=Python-urllib/3.13 status=${api_client_status}"
if [[ -n "$http_endpoint" ]]; then
  echo "public-endpoint-check: OK http_redirect=${http_endpoint} status=${redirect_status}"
fi
