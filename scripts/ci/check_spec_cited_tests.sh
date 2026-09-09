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
  # Fetching the branch tip does not guarantee base_sha itself is reachable
  # (the base branch may have been force-pushed, or the commit may predate a
  # shallow boundary). Without this check the diff below fails and, before the
  # fail-closed guard, the gate reported OK.
  git cat-file -e "${base_sha}^{commit}" 2>/dev/null || {
    echo "::error::spec-cited-tests-check: base commit $base_sha is still unavailable after fetching $base_ref" >&2
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
  git cat-file -e "${head_sha}^{commit}" 2>/dev/null || {
    echo "::error::spec-cited-tests-check: head commit $head_sha is still unavailable after fetching PR #$pr_number" >&2
    exit 1
  }
fi

owners=()
while IFS= read -r path; do
  [[ -n "$path" ]] || continue
  [[ "$path" == \#* ]] && continue
  owners+=("${path%/}")
done < "$owners_file"

# Diff from the MERGE BASE to head, so commits that landed on the base branch
# after the feature branch was created are not misclassified as PR changes.
# The two-dot form shipped in #1624 blocked PR #1634 on seven pkg/sched and
# pkg/gateway tests it never touched.
#
# The merge base is resolved explicitly rather than relying on the three-dot
# shorthand so that an unresolvable base fails here, loudly, with both SHAs in
# the message.
merge_base="$(git merge-base "$base_sha" "$head_sha" 2>/dev/null)" || {
  echo "::error::spec-cited-tests-check: no merge base between base $base_sha and head $head_sha" >&2
  exit 1
}

# Capture the diff into a variable instead of piping it into `while read`:
# inside a process substitution a git failure is invisible to `set -e`, so a
# broken diff yielded an empty file list and the gate reported OK. Fail closed.
changed_files="$(git diff --name-only --diff-filter=ACMRTUXB "$merge_base" "$head_sha")" || {
  echo "::error::spec-cited-tests-check: git diff ${merge_base}..${head_sha} failed" >&2
  exit 1
}

owned_tests=()
while IFS= read -r path; do
  [[ "$path" == *_test.go ]] || continue
  for owner in "${owners[@]}"; do
    if [[ "$path" == "$owner"/* ]]; then
      owned_tests+=("$path")
      break
    fi
  done
done <<< "$changed_files"

if ((${#owned_tests[@]} == 0)); then
  echo "spec-cited-tests-check: OK — PR changes no TESTOWNERS Go tests"
  exit 0
fi

missing=()
for path in "${owned_tests[@]}"; do
	# Do not pipe git show into grep -q under pipefail. When the citation is
	# near the start of a large test file, grep exits after the match and git
	# receives SIGPIPE; the successful citation is then reported as missing.
	content="$(git show "${head_sha}:${path}" 2>/dev/null)" || {
		missing+=("$path")
		continue
	}
	if ! grep -Eq '^[[:space:]]*//[[:space:]]*(spec:[[:space:]]*§[0-9]+([.][0-9]+)*|adr:[[:space:]]*[0-9]{3})([[:space:]]|$)' <<< "$content"; then
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
