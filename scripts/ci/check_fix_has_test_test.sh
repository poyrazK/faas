#!/usr/bin/env bash
# Hermetic tests for check_fix_has_test.sh. These use tiny temporary Git
# repositories and synthetic pull_request event payloads; no GitHub API is
# contacted.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
checker="$repo_root/scripts/ci/check_fix_has_test.sh"
test_root="$(mktemp -d)"
trap 'rm -rf "$test_root"' EXIT

git_init() {
  local dir="$1"
  mkdir -p "$dir"
  git -C "$dir" init -q -b main
  git -C "$dir" config user.email test@example.invalid
  git -C "$dir" config user.name "Fix gate test"
  printf 'base\n' > "$dir/README.md"
  git -C "$dir" add README.md
  git -C "$dir" commit -q -m 'chore: baseline'
}

make_event() {
  local dir="$1" title="$2" body="$3" labels="$4" base head
  base="$(git -C "$dir" rev-list --max-parents=0 HEAD)"
  head="$(git -C "$dir" rev-parse HEAD)"
  cat > "$dir/event.json" <<EOF
{"pull_request":{"number":42,"title":"${title}","body":"${body}","base":{"ref":"main","sha":"${base}"},"head":{"ref":"test","sha":"${head}"},"labels":${labels}}}
EOF
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

# A fix commit with a changed Go test passes.
with_test="$test_root/with-test"
git_init "$with_test"
printf 'package fixture\n' > "$with_test/fixture_test.go"
git -C "$with_test" add fixture_test.go
git -C "$with_test" commit -q -m 'fix: guard the regression'
make_event "$with_test" 'ci: enforce regression tests' '' '[]'
expect_pass 'fix commit with test' "$with_test"

# A fix-shaped PR with no test fails, even when the title itself is neutral.
without_test="$test_root/without-test"
git_init "$without_test"
printf 'changed\n' > "$without_test/README.md"
git -C "$without_test" add README.md
git -C "$without_test" commit -q -m 'fix: change behaviour'
make_event "$without_test" 'ci: enforce regression tests' '' '[]'
expect_fail 'fix commit without test' "$without_test"

# A fix title also triggers the gate when all commit subjects are neutral.
title_fix="$test_root/title-fix"
git_init "$title_fix"
printf 'changed\n' > "$title_fix/README.md"
git -C "$title_fix" add README.md
git -C "$title_fix" commit -q -m 'chore: update docs'
make_event "$title_fix" 'fix: update docs' '' '[]'
expect_fail 'fix title without test' "$title_fix"

# The explicit bypass requires both the label and a non-empty explanation.
bypass="$test_root/bypass"
git_init "$bypass"
printf 'changed\n' > "$bypass/README.md"
git -C "$bypass" add README.md
git -C "$bypass" commit -q -m 'fix: generated artifact'
make_event "$bypass" 'ci: generated artifact' 'Why no test: generated output is covered by the source contract check.' '[{"name":"no-regression-test"}]'
expect_pass 'labelled bypass with explanation' "$bypass"

empty_bypass="$test_root/empty-bypass"
git_init "$empty_bypass"
printf 'changed\n' > "$empty_bypass/README.md"
git -C "$empty_bypass" add README.md
git -C "$empty_bypass" commit -q -m 'fix: generated artifact'
make_event "$empty_bypass" 'ci: generated artifact' 'Why no test:' '[{"name":"no-regression-test"}]'
expect_fail 'labelled bypass without explanation' "$empty_bypass"

# Non-fix PRs remain green without a test file.
non_fix="$test_root/non-fix"
git_init "$non_fix"
printf 'docs\n' > "$non_fix/README.md"
git -C "$non_fix" add README.md
git -C "$non_fix" commit -q -m 'docs: clarify the workflow'
make_event "$non_fix" 'docs: clarify the workflow' '' '[]'
expect_pass 'non-fix PR' "$non_fix"

echo "check_fix_has_test: OK"
