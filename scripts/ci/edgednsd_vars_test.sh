#!/usr/bin/env bash
# Hermetic tests for edgednsd_vars.sh (ADR-20261102081618 §3), and the CD
# contract that runs it in both control-plane converges.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
script="$repo_root/scripts/ci/edgednsd_vars.sh"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }

run() { # VARS_FILE [VAR=VALUE...]
  local vars=$1
  shift
  env -i PATH="$PATH" "$@" bash "$script" "$vars"
}
token=AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-Xy

# No secret: nothing is written, even over variables that set the token.
run "$work/none.yml" || fail "no secret failed"
[[ ! -e "$work/none.yml" ]] || fail "no secret still wrote a vars file"
printf 'faas_edgednsd_cf_token_src: /operator/cf-token\n' > "$work/operator.yml"
run "$work/operator.yml" || fail "no secret refused operator-set variables"

# A secret appends the token, without its trailing newline; existing
# variables are kept and the file is private.
printf 'faas_box_role: control-plane' > "$work/cp.yml" # no trailing newline
run "$work/cp.yml" EDGEDNSD_CF_TOKEN="$token"$'\n' || fail "staging failed"
vars="$(cat "$work/cp.yml")"
[[ "$vars" == "faas_box_role: control-plane"$'\n'* ]] || fail "existing variables were not kept: $vars"
grep -qx "faas_edgednsd_cf_token: \"$token\"" "$work/cp.yml" || fail "token not staged: $vars"
mode=$(stat -c %a "$work/cp.yml" 2>/dev/null || stat -f %Lp "$work/cp.yml")
[[ "$mode" == 600 ]] || fail "vars file mode $mode, want 600"

# The operator's variables must not set the token as well.
for line in 'faas_edgednsd_cf_token: "x"' 'faas_edgednsd_cf_token_src: /operator/cf-token'; do
  printf '%s\n' "$line" > "$work/both.yml"
  if run "$work/both.yml" EDGEDNSD_CF_TOKEN="$token" 2>/dev/null; then fail "accepted a token set twice: $line"; fi
done

# Anything that is not a token is refused, so nothing can break the YAML.
for bad in 'short' 'has"quote_0123456789012345678901234567' 'has:colon_0123456789012345678901234567'; do
  if run "$work/bad.yml" EDGEDNSD_CF_TOKEN="$bad" 2>/dev/null; then fail "accepted $bad"; fi
done

# CD contract: both control-plane converges pass the secret and stage it.
workflow="$repo_root/.github/workflows/cd-controlplane.yml"
# shellcheck disable=SC2016
[[ $(grep -c 'EDGEDNSD_CF_TOKEN: ${{ secrets.EDGEDNSD_CF_TOKEN }}' "$workflow") == 2 ]] || fail "$workflow does not pass EDGEDNSD_CF_TOKEN to both converges"
# shellcheck disable=SC2016
[[ $(grep -c 'bash scripts/ci/edgednsd_vars.sh "$work/vars.yml"' "$workflow") == 2 ]] || fail "$workflow does not stage the token in both converges"

echo "edgednsd_vars_test: OK"
