#!/usr/bin/env bash
# check_regression_pin.sh — report whether changed tests fail on the PR base.
#
# This is deliberately advisory. A regression pin is strongest when the new
# test passes on the PR and fails on the merge-base, proving that it captures
# the defect being fixed. The report never blocks the PR; it classifies each
# changed Test* function as expected-failure, unexpected-pass, or missing on
# the base.

set -euo pipefail

event_name="${GITHUB_EVENT_NAME:-}"
if [[ "$event_name" != "pull_request" ]]; then
  echo "regression-pin-check: skipped (event=${event_name:-local})"
  exit 0
fi

event_path="${GITHUB_EVENT_PATH:-}"
if [[ -z "$event_path" || ! -f "$event_path" ]]; then
  echo "::warning::regression-pin-check: GITHUB_EVENT_PATH is missing; advisory skipped" >&2
  exit 0
fi
command -v jq >/dev/null 2>&1 || {
  echo "::warning::regression-pin-check: jq is unavailable; advisory skipped" >&2
  exit 0
}

base_sha="$(jq -r '.pull_request.base.sha // empty' "$event_path")"
head_sha="$(jq -r '.pull_request.head.sha // empty' "$event_path")"
pr_number="$(jq -r '.pull_request.number // empty' "$event_path")"
if [[ -z "$base_sha" || -z "$head_sha" ]]; then
  echo "::warning::regression-pin-check: pull request has no base/head SHA; advisory skipped" >&2
  exit 0
fi

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || true)"
if [[ -z "$repo_root" ]]; then
  echo "::warning::regression-pin-check: not inside a Git checkout; advisory skipped" >&2
  exit 0
fi
cd "$repo_root"

# The workflow checks out full history. Keep a bounded fallback for local
# runners that only fetched the merge commit, including fork pull requests.
if ! git cat-file -e "${base_sha}^{commit}" 2>/dev/null; then
  base_ref="$(jq -r '.pull_request.base.ref // empty' "$event_path")"
  if [[ -z "$base_ref" ]] || ! git fetch --no-tags --quiet origin "refs/heads/$base_ref:refs/remotes/origin/$base_ref"; then
    echo "::warning::regression-pin-check: base commit unavailable; advisory skipped" >&2
    exit 0
  fi
fi
if ! git cat-file -e "${head_sha}^{commit}" 2>/dev/null; then
  if [[ ! "$pr_number" =~ ^[0-9]+$ ]] || ! git fetch --no-tags --quiet origin "pull/$pr_number/head:refs/remotes/origin/pr-$pr_number-head"; then
    echo "::warning::regression-pin-check: head commit unavailable; advisory skipped" >&2
    exit 0
  fi
fi

# Use the merge-base-to-head delta. A two-dot diff also includes files that
# landed on main after the branch was created and produces false reports.
changed_files="$(git diff --name-only --diff-filter=ACMRTUXB "$base_sha...$head_sha" -- '*_test.go')"
if [[ -z "$changed_files" ]]; then
  echo "regression-pin-check: no changed Go test files"
  exit 0
fi

tmp_root="$(mktemp -d)"
baseline="$tmp_root/baseline"
# shellcheck disable=SC2329 # cleanup is invoked indirectly by trap.
cleanup() {
  git worktree remove --force "$baseline" >/dev/null 2>&1 || true
  rm -rf "$tmp_root"
}
trap cleanup EXIT

if ! git worktree add --quiet --detach "$baseline" "$base_sha"; then
  echo "::warning::regression-pin-check: could not create a base worktree; advisory skipped" >&2
  exit 0
fi

printf '%-10s %-8s %-7s %s\n' STATUS PACKAGE TEST DETAIL
printf '%-10s %-8s %-7s %s\n' ------ ------- ---- ------
found=0
while IFS= read -r path; do
  [[ -n "$path" ]] || continue
  package_dir="$(dirname "$path")"
  [[ "$package_dir" == "." ]] && package_dir=""
  package="./$package_dir"
  [[ -n "$package_dir" ]] || package="."

  # Read the PR version of each changed test file, then select the test
  # functions named by changed hunks. Git's Go xfuncname support puts the
  # enclosing function in hunk headers; added declarations cover new tests.
  diff_text="$(git diff --no-color --unified=0 "$base_sha...$head_sha" -- "$path")"
  tests="$(
    {
      printf '%s\n' "$diff_text" | sed -nE 's/^@@.*func[[:space:]]+(Test[A-Za-z0-9_]+)[(].*/\1/p'
      printf '%s\n' "$diff_text" | sed -nE 's/^\+[[:space:]]*func[[:space:]]+(Test[A-Za-z0-9_]+)[(].*/\1/p'
    } | awk 'NF && !seen[$0]++'
  )"
  [[ -n "$tests" ]] || continue
  found=1

  while IFS= read -r test_name; do
    [[ -n "$test_name" ]] || continue
    baseline_file="$baseline/$path"
    if [[ ! -f "$baseline_file" ]] || ! grep -Eq "^func[[:space:]]+${test_name}[(]" "$baseline_file"; then
      printf '%-10s %-8s %-7s %s\n' missing "$package" "$test_name" "not present on base"
      continue
    fi
    output_file="$tmp_root/output"
    set +e
    (cd "$baseline" && go test "$package" -run "^${test_name}$" -count=1) >"$output_file" 2>&1
    status=$?
    set -e
    if (( status != 0 )); then
      printf '%-10s %-8s %-7s %s\n' expected-failure "$package" "$test_name" "base exits $status"
    elif grep -q 'no tests to run' "$output_file"; then
      printf '%-10s %-8s %-7s %s\n' missing "$package" "$test_name" "not present on base"
    else
      printf '%-10s %-8s %-7s %s\n' unexpected-pass "$package" "$test_name" "base passed"
    fi
  done <<< "$tests"
done <<< "$changed_files"

if (( found == 0 )); then
  echo "regression-pin-check: changed test files had no changed Test* hunks"
else
  echo "regression-pin-check: advisory only; expected-failure rows are the useful regression pins"
fi
exit 0
