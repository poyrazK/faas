#!/usr/bin/env bash
# Exercise faas-app-edge-pilot-check.sh against a fake fleet (ADR-1343 pilot).
# The fake curl plays the public internet: a request to the control host
# makes the control plane open a connection; a pilot request does so only
# when MOCK_PILOT_VIA_CP is set, and runs a control-plane statement only when
# MOCK_STATEMENT_PER_REQUEST is set. The fake SSH answers each probe from the
# shared state.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECK="$SCRIPT_DIR/faas-app-edge-pilot-check.sh"
ROOT="$(mktemp -d)"
trap 'rm -rf "$ROOT"' EXIT
mkdir -p "$ROOT/bin"

cat >"$ROOT/bin/curl" <<'EOF'
#!/usr/bin/env bash
url="${*: -1}"
state=$MOCK_STATE
bump() { echo $(($(cat "$state/$1") + 1)) >"$state/$1"; }
case "$url" in
  *cdn-cgi/trace) echo "ip=198.51.100.7"; exit 0 ;;
  *control.gregale.dev*) bump opens ;;
  *pilot.gregale.dev/echo) printf 'x-forwarded-for: %s\n' "${MOCK_ECHO_XFF:-198.51.100.7}"; exit 0 ;;
  *pilot.gregale.dev*)
    [[ -z "${MOCK_PILOT_VIA_CP:-}" ]] || bump opens
    [[ -z "${MOCK_STATEMENT_PER_REQUEST:-}" ]] || bump stmt
    [[ -z "${MOCK_PILOT_FALLBACK:-}" ]] || bump fallback
    if [[ -n "${MOCK_PILOT_502:-}" ]]; then printf 502; exit 0; fi ;;
esac
[[ "$*" == *%{http_code}* ]] && printf 200
exit 0
EOF
cat >"$ROOT/bin/node-ssh" <<'EOF'
#!/usr/bin/env bash
node=$1
script=$(cat)
state=$MOCK_STATE
case "$script" in
  *compute-edge-check*) [[ -z "${MOCK_EDGE_DOWN:-}" ]] || exit 1; echo "fake edges ready" ;;
  */proc/net/snmp*)
    echo "opens $(cat "$state/opens")"
    printf '1111\t%s\n' "$(cat "$state/stmt")"
    printf '2222\t500\n' ;;
  *9109/metrics*)
    f=0; [[ "$node" != gregale-compute-1 ]] || f=$(cat "$state/fallback")
    echo "$node $f 0" ;;
esac
EOF
chmod +x "$ROOT/bin/curl" "$ROOT/bin/node-ssh"

run() {
  rm -rf "$ROOT/state" && mkdir -p "$ROOT/state"
  echo 1000 >"$ROOT/state/opens"; echo 7 >"$ROOT/state/stmt"; echo 0 >"$ROOT/state/fallback"
  env MOCK_STATE="$ROOT/state" CURL="$ROOT/bin/curl" NODE_SSH="$ROOT/bin/node-ssh" \
    PILOT_HOST=pilot.gregale.dev CONTROL_HOST=control.gregale.dev REQUESTS=20 "$@" \
    bash "$CHECK"
}
fail() { echo "FAIL: $*" >&2; exit 1; }
expect() { # expect <exit code> <output pattern> [env...]
  local want=$1 pattern=$2 out code=0
  shift 2
  out="$(run "$@" 2>&1)" || code=$?
  [[ $code == "$want" ]] || fail "exit $code, want $want with $*:"$'\n'"$out"
  [[ "$out" =~ $pattern ]] || fail "output does not match /$pattern/ with $*:"$'\n'"$out"
}

expect 0 "PASS: the pilot's requests did not pass through the control plane.*PASS: no statement ran once per pilot request.*PASS: gregale-compute-1: no fallback.*PASS: no 5xx"
expect 0 "PASS: the app sees 198.51.100.7" ECHO_PATH=/echo
expect 1 "FAIL: the app did not see" ECHO_PATH=/echo MOCK_ECHO_XFF=203.0.113.9
expect 1 "FAIL: the control plane opened 20 connections" MOCK_PILOT_VIA_CP=1
expect 1 "FAIL: statements that ran about once per pilot request.*1111	20" MOCK_STATEMENT_PER_REQUEST=1
expect 1 "FAIL: gregale-compute-1: 20 local-gateway fallbacks" MOCK_PILOT_FALLBACK=1
expect 1 "FAIL: pilot requests got 5xx" MOCK_PILOT_502=1
expect 1 "FAIL: a compute edge or connector is not ready" MOCK_EDGE_DOWN=1

echo "faas-app-edge-pilot-check_test: OK"
