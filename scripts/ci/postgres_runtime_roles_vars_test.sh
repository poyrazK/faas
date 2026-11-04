#!/usr/bin/env bash
set -euo pipefail

script="$(cd "$(dirname "$0")" && pwd)/postgres_runtime_roles_vars.sh"
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
fail() { echo "postgres_runtime_roles_vars: $*" >&2; exit 1; }
run() { # VARS_FILE [VAR=VALUE...]
  local vars=$1
  shift
  env -u POSTGRES_RUNTIME_ROLES_ENABLED -u POSTGRES_OWNER_CONTRACT_ENABLED "$@" bash "$script" "$vars"
}

# Unset or false: nothing is written.
run "$work/none.yml" || fail "unset failed"
[[ ! -e "$work/none.yml" ]] || fail "unset wrote a vars file"
run "$work/off.yml" POSTGRES_RUNTIME_ROLES_ENABLED=false POSTGRES_OWNER_CONTRACT_ENABLED=false || fail "false failed"
[[ ! -e "$work/off.yml" ]] || fail "false wrote a vars file"

# true appends the switch and keeps existing variables; no contract yet.
printf 'faas_domain: example.test\n' > "$work/on.yml"
run "$work/on.yml" POSTGRES_RUNTIME_ROLES_ENABLED=true || fail "true failed"
grep -qx 'faas_domain: example.test' "$work/on.yml" || fail "existing variables lost"
grep -qx 'faas_postgres_runtime_roles_enabled: true' "$work/on.yml" || fail "switch not staged"
! grep -q 'faas_postgres_owner_contracted' "$work/on.yml" || fail "contract staged without its variable"

# The contract rides on step 1.
run "$work/contract.yml" POSTGRES_RUNTIME_ROLES_ENABLED=true POSTGRES_OWNER_CONTRACT_ENABLED=true || fail "contract failed"
grep -qx 'faas_postgres_runtime_roles_enabled: true' "$work/contract.yml" || fail "contract dropped step 1"
grep -qx 'faas_postgres_owner_contracted: true' "$work/contract.yml" || fail "contract not staged"
if run "$work/alone.yml" POSTGRES_OWNER_CONTRACT_ENABLED=true 2>/dev/null; then
  fail "accepted the contract without runtime roles"
fi
[[ ! -e "$work/alone.yml" ]] || fail "a refused contract wrote a vars file"

# Anything else, or a vars file that already sets a switch, fails.
if run "$work/bad.yml" POSTGRES_RUNTIME_ROLES_ENABLED=yes 2>/dev/null; then
  fail "accepted yes"
fi
if run "$work/bad.yml" POSTGRES_RUNTIME_ROLES_ENABLED=true POSTGRES_OWNER_CONTRACT_ENABLED=1 2>/dev/null; then
  fail "accepted a contract value of 1"
fi
if run "$work/on.yml" POSTGRES_RUNTIME_ROLES_ENABLED=true 2>/dev/null; then
  fail "staged the switch twice"
fi
printf 'faas_postgres_owner_contracted: true\n' > "$work/preset.yml"
if run "$work/preset.yml" POSTGRES_RUNTIME_ROLES_ENABLED=true 2>/dev/null; then
  fail "accepted a contract set in the vars file"
fi
echo "postgres_runtime_roles_vars: ok"
