#!/usr/bin/env bash
# Hermetic tests for check_spec_cited_tests.sh. No GitHub API is contacted.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
checker="$repo_root/scripts/ci/check_spec_cited_tests.sh"
test_root="$(mktemp -d)"
trap 'rm -rf "$test_root"' EXIT

git_init() {
  local dir="$1"
  mkdir -p "$dir/pkg/sched"
  git -C "$dir" init -q -b main
  git -C "$dir" config user.email test@example.invalid
  git -C "$dir" config user.name "Spec citation gate test"
  printf 'pkg/sched\npkg/gateway\npkg/meter\npkg/billing\npkg/fcvm\n' > "$dir/TESTOWNERS"
  printf 'base\n' > "$dir/README.md"
  git -C "$dir" add .
  git -C "$dir" commit -q -m 'chore: baseline'
}

make_event() {
  local dir="$1" base head
  base="$(git -C "$dir" rev-list --max-parents=0 HEAD)"
  head="$(git -C "$dir" rev-parse HEAD)"
  cat > "$dir/event.json" <<EOF_EVENT
{"pull_request":{"number":43,"base":{"ref":"main","sha":"${base}"},"head":{"ref":"test","sha":"${head}"}}}
EOF_EVENT
}

run_check() {
  local dir="$1"
  (cd "$dir" && GITHUB_EVENT_NAME=pull_request GITHUB_EVENT_PATH="$dir/event.json" bash "$checker")
}

expect_pass() {
  local name="$1" dir="$2"
  if ! run_check "$dir" >"$dir/stdout" 2>"$dir/stderr"; then
    echo "FAIL: ${name} unexpectedly failed" >&2
    cat "$dir/stderr" >&2
    exit 1
  fi
}

expect_fail() {
  local name="$1" dir="$2"
  if run_check "$dir" >"$dir/stdout" 2>"$dir/stderr"; then
    echo "FAIL: ${name} unexpectedly passed" >&2
    exit 1
  fi
}

# A spec citation satisfies the gate.
spec_case="$test_root/spec"
git_init "$spec_case"
printf 'package sched\n\n// spec: §6.2\nfunc TestWake(t *testing.T) {}\n' > "$spec_case/pkg/sched/wake_test.go"
git -C "$spec_case" add pkg/sched/wake_test.go
git -C "$spec_case" commit -q -m 'test(sched): pin wake admission'
make_event "$spec_case"
expect_pass 'spec citation' "$spec_case"

# ADR citations are accepted as an alternative.
adr_case="$test_root/adr"
git_init "$adr_case"
printf 'package sched\n\n// adr: 098\nfunc TestWake(t *testing.T) {}\n' > "$adr_case/pkg/sched/wake_test.go"
git -C "$adr_case" add pkg/sched/wake_test.go
git -C "$adr_case" commit -q -m 'test(sched): pin wake admission'
make_event "$adr_case"
expect_pass 'ADR citation' "$adr_case"

# A changed core-path test without a citation is rejected.
missing_case="$test_root/missing"
git_init "$missing_case"
printf 'package sched\n\nfunc TestWake(t *testing.T) {}\n' > "$missing_case/pkg/sched/wake_test.go"
git -C "$missing_case" add pkg/sched/wake_test.go
git -C "$missing_case" commit -q -m 'test(sched): pin wake admission'
make_event "$missing_case"
expect_fail 'missing citation' "$missing_case"

# Production-only changes and tests outside TESTOWNERS remain unaffected.
production_case="$test_root/production"
git_init "$production_case"
printf 'changed\n' > "$production_case/pkg/sched/engine.go"
git -C "$production_case" add pkg/sched/engine.go
git -C "$production_case" commit -q -m 'feat(sched): adjust admission'
make_event "$production_case"
expect_pass 'production-only change' "$production_case"

outside_case="$test_root/outside"
git_init "$outside_case"
mkdir -p "$outside_case/pkg/other"
printf 'package other\n\nfunc TestOther(t *testing.T) {}\n' > "$outside_case/pkg/other/other_test.go"
git -C "$outside_case" add pkg/other/other_test.go
git -C "$outside_case" commit -q -m 'test(other): add coverage'
make_event "$outside_case"
expect_pass 'non-owned test' "$outside_case"

echo "check_spec_cited_tests: OK"
