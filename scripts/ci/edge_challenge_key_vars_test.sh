#!/usr/bin/env bash
# Hermetic tests for edge_challenge_key_vars.sh (ADR-831 amendment 17).
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
script="$repo_root/scripts/ci/edge_challenge_key_vars.sh"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }

run() { # VARS_FILE [VAR=VALUE...]
  local vars=$1
  shift
  env -i PATH="$PATH" "$@" bash "$script" "$vars"
}
key=0123456789ABCDEF0123456789abcdef0123456789abcdef0123456789ABCDEF
lower="$(printf '%s' "$key" | tr 'A-F' 'a-f')"

# No secret: nothing is written, even over variables that set the key.
run "$work/none.yml" || fail "no secret failed"
[[ ! -e "$work/none.yml" ]] || fail "no secret still wrote a vars file"
printf 'gatewayd_edge_challenge_key: "%s"\n' "$lower" > "$work/operator.yml"
run "$work/operator.yml" || fail "no secret refused operator-set variables"

# A secret appends the key, lower-cased, and makes it required; existing
# variables are kept.
printf 'faas_box_role: compute' > "$work/compute.yml" # no trailing newline
run "$work/compute.yml" GATEWAYD_EDGE_CHALLENGE_KEY="$key" || fail "staging failed"
vars="$(cat "$work/compute.yml")"
[[ "$vars" == "faas_box_role: compute"$'\n'* ]] || fail "existing variables were not kept: $vars"
grep -qx "gatewayd_edge_challenge_key: \"$lower\"" "$work/compute.yml" || fail "key not staged: $vars"
grep -qx 'gatewayd_edge_challenge_key_required: true' "$work/compute.yml" || fail "key not required: $vars"
[[ "$(stat -c %a "$work/compute.yml" 2>/dev/null || stat -f %Lp "$work/compute.yml")" == 600 ]] || fail "vars file is not 0600"

# A missing vars file is created.
run "$work/new.yml" GATEWAYD_EDGE_CHALLENGE_KEY="$key" || fail "staging into a new file failed"
grep -qx 'gatewayd_edge_challenge_key_required: true' "$work/new.yml" || fail "new file lacks the key"

# A malformed secret and a conflict with the operator variables fail.
if run "$work/bad.yml" GATEWAYD_EDGE_CHALLENGE_KEY=not-hex 2>/dev/null; then
  fail "a malformed key was accepted"
fi
if run "$work/operator.yml" GATEWAYD_EDGE_CHALLENGE_KEY="$key" 2>/dev/null; then
  fail "a key also set in the operator variables was accepted"
fi
if run "$work/compute.yml" GATEWAYD_EDGE_CHALLENGE_KEY="$key" 2>/dev/null; then
  fail "staging twice was accepted"
fi

echo "edge_challenge_key_vars: ok"
