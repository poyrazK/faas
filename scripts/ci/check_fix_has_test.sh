#!/usr/bin/env bash
# check_fix_has_test.sh — require a regression test for fix-shaped PRs.
#
# A fix commit or PR title is a claim that production behaviour changed. The
# PR must carry a changed Go test file so the claim has a durable regression
# pin. The only escape hatch is an explicit no-regression-test label plus a
# non-empty Why no test: explanation.
#
# The check is scoped to pull_request events. Pushes and local invocations
# without GitHub event metadata are intentionally no-ops.

set -euo pipefail

event_name="${GITHUB_EVENT_NAME:-}"
if [[ "$event_name" != "pull_request" ]]; then
  echo "fix-has-test-check: skipped (event=${event_name:-local})"
  exit 0
fi

event_path="${GITHUB_EVENT_PATH:-}"
if [[ -z "$event_path" || ! -f "$event_path" ]]; then
  echo "::error::fix-has-test-check: GITHUB_EVENT_PATH is missing for a pull_request event"
  exit 1
fi
command -v jq >/dev/null 2>&1 || {
  echo "::error::fix-has-test-check: jq is required to read pull request metadata" >&2
  exit 1
}

pr_title="$(jq -r '.pull_request.title // empty' "$event_path")"
pr_body="$(jq -r '.pull_request.body // empty' "$event_path")"
pr_number="$(jq -r '.pull_request.number // empty' "$event_path")"
base_sha="$(jq -r '.pull_request.base.sha // empty' "$event_path")"
head_sha="$(jq -r '.pull_request.head.sha // empty' "$event_path")"

[[ -n "$base_sha" && -n "$head_sha" ]] || {
  echo "::error::fix-has-test-check: pull request event has no base/head commit SHA" >&2
  exit 1
}

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || true)"
[[ -n "$repo_root" ]] || {
  echo "::error::fix-has-test-check: must run inside a Git checkout" >&2
  exit 1
}
cd "$repo_root"

# The CI job checks out full history. Keep a bounded fallback for local runners
# that only fetched the merge commit, including PRs from a fork.
if ! git cat-file -e "${base_sha}^{commit}" 2>/dev/null; then
  base_ref="$(jq -r '.pull_request.base.ref // empty' "$event_path")"
  [[ -n "$base_ref" ]] || {
    echo "::error::fix-has-test-check: base commit $base_sha is unavailable and the event has no base ref" >&2
    exit 1
  }
  git fetch --no-tags --quiet origin "refs/heads/$base_ref:refs/remotes/origin/$base_ref" || {
    echo "::error::fix-has-test-check: could not fetch base commit $base_sha" >&2
    exit 1
  }
fi
if ! git cat-file -e "${head_sha}^{commit}" 2>/dev/null; then
  [[ "$pr_number" =~ ^[0-9]+$ ]] || {
    echo "::error::fix-has-test-check: head commit $head_sha is unavailable and PR number is missing" >&2
    exit 1
  }
  git fetch --no-tags --quiet origin \
    "pull/$pr_number/head:refs/remotes/origin/pr-$pr_number-head" || {
    echo "::error::fix-has-test-check: could not fetch head commit $head_sha for PR #$pr_number" >&2
    exit 1
  }
fi

trigger=""
if [[ "$pr_title" == fix* ]]; then
  trigger="PR title '$pr_title'"
fi
while IFS= read -r subject; do
  [[ -n "$subject" ]] || continue
  if [[ "$subject" == fix* ]]; then
    if [[ -n "$trigger" ]]; then
      trigger+="; commit '$subject'"
    else
      trigger="commit '$subject'"
    fi
  fi
done < <(git log --format=%s "${base_sha}..${head_sha}")

if [[ -z "$trigger" ]]; then
  echo "fix-has-test-check: OK — PR #${pr_number:-unknown} has no fix-shaped title or commit"
  exit 0
fi

changed_tests="$(git diff --name-only --diff-filter=ACMRTUXB "$base_sha" "$head_sha" -- '*_test.go')"
if [[ -n "$changed_tests" ]]; then
  echo "fix-has-test-check: OK — $trigger changes Go test coverage:"
  printf '  %s\n' "$changed_tests"
  exit 0
fi

has_bypass_label=0
while IFS= read -r label; do
  if [[ "$label" == "no-regression-test" ]]; then
    has_bypass_label=1
    break
  fi
done < <(jq -r '.pull_request.labels[]?.name // empty' "$event_path")

if (( has_bypass_label == 1 )) && printf '%s\n' "$pr_body" \
  | grep -Eq '(^|[[:space:]])Why no test:[[:space:]]*[^[:space:]]'; then
  echo "::warning::fix-has-test-check: $trigger bypassed by no-regression-test with a Why no test explanation"
  exit 0
fi

echo "::error::fix-has-test-check: $trigger must change a *_test.go file" >&2
echo "::error::Add a regression test to this PR. For an intentional exception, add the no-regression-test label and a non-empty Why no test: line to the PR body." >&2
[[ -z "$pr_number" ]] || echo "::error::PR #$pr_number changed files:" >&2
git diff --name-only "$base_sha" "$head_sha" -- | sed 's/^/  /' >&2
exit 1
