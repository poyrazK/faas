#!/usr/bin/env bash

set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
checker="${repo_root}/scripts/ci/migration-full-suite-needed.sh"

expect_needed() {
  local paths=$1
  if ! printf '%s\n' "$paths" | "$checker"; then
    printf 'expected migration impact for:\n%s\n' "$paths" >&2
    exit 1
  fi
}

expect_not_needed() {
  local paths=$1
  if printf '%s\n' "$paths" | "$checker"; then
    printf 'unexpected migration impact for:\n%s\n' "$paths" >&2
    exit 1
  fi
}

expect_needed 'migrations/20260914000000000_example.sql'
expect_needed 'migrations/example_test.go'
expect_needed 'pkg/db/pgtest/pgtest.go'
expect_needed 'pkg/state/pgstore.go'
expect_needed 'go.mod'
expect_needed $'docs/operations.md\npkg/api/types.go\n.github/workflows/cd-controlplane.yml'
expect_needed './pkg/reqbudget/budget.go'

expect_not_needed ''
expect_not_needed '.github/workflows/cd-controlplane.yml'
expect_not_needed $'docs/operations.md\ncmd/gregalectl/fleet_seal_cd_contract_test.go'
expect_not_needed 'web/src/app.tsx'

echo 'migration full-suite impact checker tests passed'
