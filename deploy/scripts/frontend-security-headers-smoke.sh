#!/bin/sh
# Checks the frontend's security headers on the apex, the dashboard and a
# client-side deep route. Permissions-Policy features may come in any order,
# and the deep route may answer 200 (SPA fallback) or 404 (static host) as
# long as it carries the same headers (issue #2720).
set -eu

base_url=${1:-https://gregale.dev}
curl_bin=${FAAS_CURL:-curl}
tmp_headers=$(mktemp)
trap 'rm -f "$tmp_headers"' EXIT HUP INT TERM

fail() {
  echo "$1" >&2
  exit 1
}

# header_value prints header $1 of the final response, lower-cased: with
# --location curl dumps every hop's headers, and the last one is served.
header_value() {
  grep -i "^$1:" "$tmp_headers" | tail -n 1 | cut -d: -f2- | tr -d '\r' |
    sed 's/^[[:space:]]*//; s/[[:space:]]*$//' | tr '[:upper:]' '[:lower:]'
}

check_headers() {
  path=$1
  case "$(header_value content-security-policy)" in
    *"frame-ancestors 'none'"*) ;;
    *) fail "missing or invalid content-security-policy on ${path}" ;;
  esac
  [ "$(header_value x-content-type-options)" = nosniff ] ||
    fail "missing or invalid x-content-type-options on ${path}"
  [ "$(header_value referrer-policy)" = strict-origin-when-cross-origin ] ||
    fail "missing or invalid referrer-policy on ${path}"
  policy=$(header_value permissions-policy | tr -d ' ')
  for feature in camera microphone geolocation payment usb; do
    case ",${policy}," in
      *",${feature}=(),"*) ;;
      *) fail "permissions-policy on ${path} does not disable ${feature}" ;;
    esac
  done
  [ "$(header_value x-frame-options)" = deny ] ||
    fail "missing or invalid x-frame-options on ${path}"
  if [ "$(header_value access-control-allow-origin)" = "*" ]; then
    fail "wildcard document CORS remains enabled on ${path}"
  fi
}

for path in / /dashboard /dashboard/apps/security-smoke; do
  status=$("$curl_bin" --silent --show-error --location \
    --dump-header "$tmp_headers" --output /dev/null --write-out '%{http_code}' \
    "${base_url}${path}") || fail "could not fetch ${path}"
  case "${path}:${status}" in
    /dashboard/apps/security-smoke:200 | /dashboard/apps/security-smoke:404) ;;
    /dashboard/apps/security-smoke:*) fail "${path} answered ${status}, want 200 or 404" ;;
    *:2??) ;;
    *) fail "${path} answered ${status}" ;;
  esac
  check_headers "$path"
done

echo "frontend security headers: ok"
