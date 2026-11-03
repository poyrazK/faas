#!/usr/bin/env bash
set -euo pipefail

script="$(cd "$(dirname "$0")" && pwd)/postgres_runtime_roles_vars.sh"
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
fail() { echo "postgres_runtime_roles_vars: $*" >&2; exit 1; }
run() { # VARS_FILE [VAR=VALUE...]
  local vars=$1
  shift
  env -u POSTGRES_RUNTIME_ROLES_ENABLED "$@" bash "$script" "$vars"
}

# Unset or false: nothing is written.
run "$work/none.yml" || fail "unset failed"
[[ ! -e "$work/none.yml" ]] || fail "unset wrote a vars file"
run "$work/off.yml" POSTGRES_RUNTIME_ROLES_ENABLED=false || fail "false failed"
[[ ! -e "$work/off.yml" ]] || fail "false wrote a vars file"

# true appends the switch and keeps existing variables.
printf 'faas_domain: example.test\n' > "$work/on.yml"
run "$work/on.yml" POSTGRES_RUNTIME_ROLES_ENABLED=true || fail "true failed"
grep -qx 'faas_domain: example.test' "$work/on.yml" || fail "existing variables lost"
grep -qx 'faas_postgres_runtime_roles_enabled: true' "$work/on.yml" || fail "switch not staged"

# Anything else, or a vars file that already sets the switch, fails.
if run "$work/bad.yml" POSTGRES_RUNTIME_ROLES_ENABLED=yes 2>/dev/null; then
  fail "accepted yes"
fi
if run "$work/on.yml" POSTGRES_RUNTIME_ROLES_ENABLED=true 2>/dev/null; then
  fail "staged the switch twice"
fi
echo "postgres_runtime_roles_vars: ok"
