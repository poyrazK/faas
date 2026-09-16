#!/bin/sh
set -eu

base_url=${1:-https://gregale.dev}
tmp_headers=$(mktemp)
trap 'rm -f "$tmp_headers"' EXIT HUP INT TERM

for path in / /dashboard /dashboard/apps/security-smoke; do
  curl --fail --silent --show-error --location \
    --dump-header "$tmp_headers" --output /dev/null "${base_url}${path}"

  require_header() {
    name=$1
    pattern=$2
    if ! grep -Eiq "^${name}:[[:space:]]*${pattern}" "$tmp_headers"; then
      echo "missing or invalid ${name} on ${path}" >&2
      exit 1
    fi
  }

  require_header content-security-policy ".*frame-ancestors 'none'"
  require_header x-content-type-options 'nosniff'
  require_header referrer-policy 'strict-origin-when-cross-origin'
  require_header permissions-policy 'camera=\(\), microphone=\(\), geolocation=\(\), payment=\(\), usb=\(\)'
  require_header x-frame-options 'DENY'
  if grep -Eiq '^access-control-allow-origin:[[:space:]]*\*' "$tmp_headers"; then
    echo "wildcard document CORS remains enabled on ${path}" >&2
    exit 1
  fi
done

echo "frontend security headers: ok"
