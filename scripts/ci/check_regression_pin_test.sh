#!/usr/bin/env bash
# Hermetic tests for check_regression_pin.sh. No GitHub API or Postgres is used.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
checker="$repo_root/scripts/ci/check_regression_pin.sh"
test_root="$(mktemp -d)"
trap 'rm -rf "$test_root"' EXIT

git_init() {
  local dir="$1"
  mkdir -p "$dir"
  git -C "$dir" init -q -b main
  git -C "$dir" config user.email test@example.invalid
  git -C "$dir" config user.name "Regression pin test"
  cat > "$dir/go.mod" <<'EOF'
module example.invalid/pin

go 1.23
EOF
  git -C "$dir" add go.mod
  git -C "$dir" commit -q -m 'chore: baseline'
}

make_event_at() {
  local dir="$1" title="$2" base="$3" head
  head="$(git -C "$dir" rev-parse HEAD)"
  cat > "$dir/event.json" <<EOF
{"pull_request":{"number":42,"title":"${title}","base":{"ref":"main","sha":"${base}"},"head":{"ref":"test","sha":"${head}"}}}
EOF
}

make_event() {
  local dir="$1" title="$2" base
  base="$(git -C "$dir" rev-list --max-parents=0 HEAD)"
  make_event_at "$dir" "$title" "$base"
}

run_check() {
  local dir="$1"
  (cd "$dir" && GITHUB_EVENT_NAME=pull_request GITHUB_EVENT_PATH="$dir/event.json" bash "$checker")
}

# A changed test that exists on the base and fails there is reported as the
# useful regression pin. The PR changes only the assertion body, so the test
# name is discovered from the enclosing diff hunk.
expected="$test_root/expected"
git_init "$expected"
cat > "$expected/pin_test.go" <<'EOF'
package pin

import "testing"

func TestRegressionPin(t *testing.T) { t.Fatal("old bug") }
EOF
git -C "$expected" add pin_test.go
git -C "$expected" commit -q -m 'test: add baseline pin'
cat > "$expected/pin_test.go" <<'EOF'
package pin

import "testing"

func TestRegressionPin(t *testing.T) { if false { t.Fatal("old bug") } }
EOF
git -C "$expected" add pin_test.go
git -C "$expected" commit -q -m 'test: make the pin pass after the fix'
make_event_at "$expected" 'test: add regression pin' "$(git -C "$expected" rev-parse HEAD^)"
run_check "$expected" > "$expected/output"
grep -q 'expected-failure.*TestRegressionPin' "$expected/output"

# A newly introduced test is explicitly classified as missing on the base,
# rather than being mistaken for a passing baseline.
missing="$test_root/missing"
git_init "$missing"
cat > "$missing/new_test.go" <<'EOF'
package pin

import "testing"

func TestNewPin(t *testing.T) {}
EOF
git -C "$missing" add new_test.go
git -C "$missing" commit -q -m 'test: add new pin'
make_event "$missing" 'test: add regression pin'
run_check "$missing" > "$missing/output"
grep -q 'missing.*TestNewPin' "$missing/output"

# A changed test that still passes on the base is visible as an unexpected
# pass, which tells the reviewer the new test may not be pinning a defect.
passing="$test_root/passing"
git_init "$passing"
cat > "$passing/pin_test.go" <<'EOF'
package pin

import "testing"

func TestUnpinned(t *testing.T) {}
EOF
git -C "$passing" add pin_test.go
git -C "$passing" commit -q -m 'test: add passing test'
printf 'package pin\n\nimport "testing"\n\nfunc TestUnpinned(t *testing.T) { t.Log("changed") }\n' > "$passing/pin_test.go"
git -C "$passing" add pin_test.go
git -C "$passing" commit -q -m 'test: adjust passing test'
make_event_at "$passing" 'test: add regression pin' "$(git -C "$passing" rev-parse HEAD^)"
run_check "$passing" > "$passing/output"
grep -q 'unexpected-pass.*TestUnpinned' "$passing/output"

echo "check_regression_pin: OK"
