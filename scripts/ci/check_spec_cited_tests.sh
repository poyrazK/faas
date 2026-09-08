#!/usr/bin/env bash
# Require changed tests in TESTOWNERS paths to cite a spec section or ADR.

set -euo pipefail

event_name="${GITHUB_EVENT_NAME:-}"
if [[ "$event_name" != "pull_request" ]]; then
  echo "spec-cited-tests-check: skipped (event=${event_name:-local})"
  exit 0
fi

event_path="${GITHUB_EVENT_PATH:-}"
if [[ -z "$event_path" || ! -f "$event_path" ]]; then
  echo "::error::spec-cited-tests-check: GITHUB_EVENT_PATH is missing for a pull_request event" >&2
  exit 1
fi
command -v jq >/dev/null 2>&1 || {
  echo "::error::spec-cited-tests-check: jq is required to read pull request metadata" >&2
  exit 1
}

base_sha="$(jq -r '.pull_request.base.sha // empty' "$event_path")"
head_sha="$(jq -r '.pull_request.head.sha // empty' "$event_path")"
pr_number="$(jq -r '.pull_request.number // empty' "$event_path")"
[[ -n "$base_sha" && -n "$head_sha" ]] || {
  echo "::error::spec-cited-tests-check: pull request event has no base/head commit SHA" >&2
  exit 1
}

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || true)"
[[ -n "$repo_root" ]] || {
  echo "::error::spec-cited-tests-check: must run inside a Git checkout" >&2
  exit 1
}
cd "$repo_root"

owners_file="${SPEC_TEST_OWNERS_FILE:-TESTOWNERS}"
[[ -f "$owners_file" ]] || {
  echo "::error::spec-cited-tests-check: missing $owners_file" >&2
  exit 1
}

# CI checks out full history. Keep bounded fetch fallbacks for shallow runners.
if ! git cat-file -e "${base_sha}^{commit}" 2>/dev/null; then
  base_ref="$(jq -r '.pull_request.base.ref // empty' "$event_path")"
  [[ -n "$base_ref" ]] || {
    echo "::error::spec-cited-tests-check: base commit $base_sha is unavailable and the event has no base ref" >&2
    exit 1
  }
  git fetch --no-tags --quiet origin "refs/heads/$base_ref:refs/remotes/origin/$base_ref" || {
    echo "::error::spec-cited-tests-check: could not fetch base commit $base_sha" >&2
    exit 1
  }
fi
if ! git cat-file -e "${head_sha}^{commit}" 2>/dev/null; then
  [[ "$pr_number" =~ ^[0-9]+$ ]] || {
    echo "::error::spec-cited-tests-check: head commit $head_sha is unavailable and PR number is missing" >&2
    exit 1
  }
  git fetch --no-tags --quiet origin \
    "pull/$pr_number/head:refs/remotes/origin/pr-$pr_number-head" || {
    echo "::error::spec-cited-tests-check: could not fetch head commit $head_sha for PR #$pr_number" >&2
    exit 1
  }
fi

owners=()
while IFS= read -r path; do
  [[ -n "$path" ]] || continue
  [[ "$path" == \#* ]] && continue
  owners+=("${path%/}")
done < "$owners_file"

owned_tests=()
while IFS= read -r path; do
  [[ "$path" == *_test.go ]] || continue
  for owner in "${owners[@]}"; do
    if [[ "$path" == "$owner"/* ]]; then
      owned_tests+=("$path")
      break
    fi
  done
done < <(git diff --name-only --diff-filter=ACMRTUXB "$base_sha" "$head_sha")

if ((${#owned_tests[@]} == 0)); then
  echo "spec-cited-tests-check: OK — PR changes no TESTOWNERS Go tests"
  exit 0
fi

missing=()
for path in "${owned_tests[@]}"; do
  if ! git show "${head_sha}:${path}" 2>/dev/null \
    | grep -Eq '^[[:space:]]*//[[:space:]]*(spec:[[:space:]]*§[0-9]+([.][0-9]+)*|adr:[[:space:]]*[0-9]{3})([[:space:]]|$)'; then
    missing+=("$path")
  fi
done

if ((${#missing[@]} > 0)); then
  echo "::error::spec-cited-tests-check: changed core-path tests must cite // spec: §x.y or // adr: NNN" >&2
  printf '  %s\n' "${missing[@]}" >&2
  exit 1
fi

echo "spec-cited-tests-check: OK — cited core-path tests:"
printf '  %s\n' "${owned_tests[@]}"
