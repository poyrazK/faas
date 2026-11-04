#!/usr/bin/env bash
# Hermetic tests for faas_environment_vars.sh (ADR-20261101214152), and the CD
# contract that runs it in both control-plane converges.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
script="$repo_root/scripts/ci/faas_environment_vars.sh"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }

run() { # VARS_FILE [VAR=VALUE...]
  local vars=$1
  shift
  env -i PATH="$PATH" "$@" bash "$script" "$vars"
}

# Production environments stage "production", keeping existing variables.
for environment in production production-us; do
  printf 'faas_box_role: control-plane\n' > "$work/$environment.yml"
  run "$work/$environment.yml" DEPLOY_ENVIRONMENT="$environment" || fail "$environment failed"
  grep -qx 'faas_box_role: control-plane' "$work/$environment.yml" || fail "$environment dropped existing variables"
  grep -qx 'faas_environment: production' "$work/$environment.yml" || fail "$environment staged no environment"
done

# Other environments, and none, stage nothing.
for environment in staging scenario-acceptance ""; do
  run "$work/other.yml" DEPLOY_ENVIRONMENT="$environment" || fail "'$environment' failed"
  [[ ! -e "$work/other.yml" ]] || fail "'$environment' wrote a vars file"
done

# An environment's own variable wins.
printf 'faas_environment: staging\n' > "$work/operator.yml"
run "$work/operator.yml" DEPLOY_ENVIRONMENT=production || fail "operator variable failed"
[[ "$(grep -c '^faas_environment:' "$work/operator.yml")" == 1 ]] || fail "staged a second faas_environment"

# Both control-plane converges stage it.
count=$(grep -c 'bash scripts/ci/faas_environment_vars.sh "\$work/vars.yml"' "$repo_root/.github/workflows/cd-controlplane.yml")
[[ "$count" == 2 ]] || fail "cd-controlplane.yml runs the script $count times, want 2"

echo "faas_environment_vars: ok"
