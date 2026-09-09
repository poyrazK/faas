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

make_event_refs() {
  local dir="$1" base="$2" head="$3"
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

# Commits that land on main after the feature branch diverges are not PR
# changes. The old two-dot diff treated this base-only, uncited test as a
# changed file and blocked an otherwise valid pull request.
base_advanced_case="$test_root/base-advanced"
git_init "$base_advanced_case"
git -C "$base_advanced_case" checkout -q -b feature
printf 'changed\n' > "$base_advanced_case/pkg/sched/engine.go"
git -C "$base_advanced_case" add pkg/sched/engine.go
git -C "$base_advanced_case" commit -q -m 'feat(sched): adjust admission'
feature_head="$(git -C "$base_advanced_case" rev-parse HEAD)"
git -C "$base_advanced_case" checkout -q main
mkdir -p "$base_advanced_case/pkg/sched"
printf 'package sched\n\nfunc TestBaseOnly(t *testing.T) {}\n' > "$base_advanced_case/pkg/sched/base_only_test.go"
git -C "$base_advanced_case" add pkg/sched/base_only_test.go
git -C "$base_advanced_case" commit -q -m 'test(sched): add base coverage'
base_head="$(git -C "$base_advanced_case" rev-parse HEAD)"
make_event_refs "$base_advanced_case" "$base_head" "$feature_head"
expect_pass 'base-only test is not a PR change' "$base_advanced_case"

# A base SHA that is not reachable must fail the gate, not pass it. Before the
# fail-closed guard the diff errored, the file list came back empty, and the
# checker printed OK — a false pass in a required check.
unreachable_case="$test_root/unreachable-base"
git_init "$unreachable_case"
mkdir -p "$unreachable_case/pkg/sched"
printf 'package sched\n\nfunc TestUncited(t *testing.T) {}\n' > "$unreachable_case/pkg/sched/uncited_test.go"
git -C "$unreachable_case" add pkg/sched/uncited_test.go
git -C "$unreachable_case" commit -q -m 'test(sched): uncited'
make_event_refs "$unreachable_case" "0000000000000000000000000000000000000000" "$(git -C "$unreachable_case" rev-parse HEAD)"
expect_fail 'unreachable base sha fails closed' "$unreachable_case"

# Two histories with no common ancestor must also fail closed rather than
# silently diffing everything or nothing.
unrelated_case="$test_root/unrelated-histories"
git_init "$unrelated_case"
unrelated_base="$(git -C "$unrelated_case" rev-parse HEAD)"
git -C "$unrelated_case" checkout -q --orphan orphan
git -C "$unrelated_case" rm -rq --cached . 2>/dev/null || true
printf 'pkg/sched\npkg/gateway\npkg/meter\npkg/billing\npkg/fcvm\n' > "$unrelated_case/TESTOWNERS"
mkdir -p "$unrelated_case/pkg/sched"
printf 'package sched\n\nfunc TestOrphan(t *testing.T) {}\n' > "$unrelated_case/pkg/sched/orphan_test.go"
git -C "$unrelated_case" add TESTOWNERS pkg/sched/orphan_test.go
git -C "$unrelated_case" commit -q -m 'test(sched): orphan history'
make_event_refs "$unrelated_case" "$unrelated_base" "$(git -C "$unrelated_case" rev-parse HEAD)"
expect_fail 'unrelated histories fail closed' "$unrelated_case"

echo "check_spec_cited_tests: OK"
