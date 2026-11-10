#!/usr/bin/env bash
# Tests for scripts/ci/faas_billing_mode.sh.
set -euo pipefail
script="$(cd "$(dirname "$0")" && pwd)/faas_billing_mode.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fail() {
  echo "FAIL: $*" >&2
  exit 1
}

[[ "$(FAAS_BILLING_MODE_SETTING='' bash "$script" resolve)" == disabled ]] || fail "unset resolves to disabled"
[[ "$(FAAS_BILLING_MODE_SETTING=live bash "$script" resolve)" == live ]] || fail "live resolves to live"
if FAAS_BILLING_MODE_SETTING=on bash "$script" resolve 2>/dev/null; then fail "invalid mode accepted"; fi

: >"$tmp/vars.yml"
FAAS_BILLING_MODE_SETTING=live bash "$script" vars "$tmp/vars.yml"
grep -qx 'faas_billing_mode: live' "$tmp/vars.yml" || fail "vars not staged"
FAAS_BILLING_MODE_SETTING=live bash "$script" vars "$tmp/vars.yml"
[[ $(grep -c '^faas_billing_mode:' "$tmp/vars.yml") -eq 1 ]] || fail "staged twice"

printf 'faas_billing_mode: "disabled"\n' >"$tmp/conflict.yml"
if FAAS_BILLING_MODE_SETTING=live bash "$script" vars "$tmp/conflict.yml" 2>/dev/null; then fail "conflicting vars accepted"; fi
FAAS_BILLING_MODE_SETTING=disabled bash "$script" vars "$tmp/conflict.yml" || fail "matching vars refused"
echo "faas_billing_mode tests passed"
