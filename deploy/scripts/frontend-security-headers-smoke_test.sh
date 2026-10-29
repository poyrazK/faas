#!/usr/bin/env bash
# Exercise frontend-security-headers-smoke.sh against a fake curl (issue #2720).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SMOKE="$SCRIPT_DIR/frontend-security-headers-smoke.sh"
ROOT="$(mktemp -d)"
trap 'rm -rf "$ROOT"' EXIT

# The fake curl writes MOCK_HEADERS (one header per line) as the response
# headers, answers MOCK_DEEP_STATUS for the deep route and 200 otherwise, and
# prints the status for --write-out.
cat >"$ROOT/curl" <<'EOF'
#!/usr/bin/env bash
dump="" url=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --dump-header) dump=$2; shift 2 ;;
    --output|--write-out) shift 2 ;;
    -*) shift ;;
    *) url=$1; shift ;;
  esac
done
status=200
[[ "$url" == */dashboard/apps/security-smoke ]] && status=${MOCK_DEEP_STATUS:-200}
{ printf 'HTTP/2 %s\r\n' "$status"; printf '%s\n' "$MOCK_HEADERS" | sed 's/$/\r/'; } >"$dump"
printf '%s' "$status"
EOF
chmod +x "$ROOT/curl"

production="content-security-policy: default-src 'self'; frame-ancestors 'none'
x-content-type-options: nosniff
referrer-policy: strict-origin-when-cross-origin
permissions-policy: camera=(), geolocation=(), microphone=(), payment=(), usb=()
x-frame-options: DENY"

run() {
  FAAS_CURL="$ROOT/curl" MOCK_HEADERS="$1" MOCK_DEEP_STATUS="${2:-200}" sh "$SMOKE" http://smoke.test
}

pass() {
  local name=$1; shift
  if ! out="$(run "$@" 2>&1)"; then
    echo "FAIL: $name should pass: $out" >&2; exit 1
  fi
}

refuse() {
  local name=$1 want=$2; shift 2
  if out="$(run "$@" 2>&1)"; then
    echo "FAIL: $name should fail" >&2; exit 1
  fi
  [[ "$out" == *"$want"* ]] || { echo "FAIL: $name: got $out, want $want" >&2; exit 1; }
}

pass "production header order" "$production"
pass "reordered permissions-policy" "${production/camera=(), geolocation=(), microphone=(), payment=(), usb=()/usb=(), payment=(), microphone=(), camera=(), geolocation=()}"
pass "deep route answers 404 with the headers" "$production" 404
refuse "usb not disabled" "does not disable usb" "${production/, usb=()/}"
refuse "wildcard CORS" "wildcard document CORS" "$production
access-control-allow-origin: *"
refuse "deep route 500" "answered 500" "$production" 500
refuse "missing frame-ancestors" "content-security-policy" "${production/frame-ancestors/frame-src}"

echo "frontend-security-headers-smoke_test: ok"
