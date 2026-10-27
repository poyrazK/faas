#!/usr/bin/env bash
# Lint only what a change affects, as ci-light does: golangci-lint (govet
# included) on each changed package, then teststorecheck on the changed test
# files and sealdomaincheck on the tree. `make lint` lints all ~520 packages
# one by one; a single large package such as pkg/state takes minutes and
# several GB.
#
# The change is everything that differs from the merge base with origin/main:
# commits, uncommitted edits and untracked files. golangci-lint waits for
# another session's run instead of starting a second one beside it.
# Environment:
#   BASE=<ref>   compare against <ref> instead of the merge base
#   DRY_RUN=1    print the selection without running anything
#   GOMEMLIMIT   set by `make lint-changed`
#   GOFLAGS      set by `make lint-changed` to the Makefile's GO_DEV_GOFLAGS, so
#                lint reuses the packages `make test-changed` compiled
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"
go_bin="${GO:-go}"

base="${BASE:-$(git merge-base HEAD origin/main)}"
paths="$({ git diff --name-only "$base"; git ls-files --others --exclude-standard; } | sort -u)"
if [[ -z "$paths" ]]; then
  echo "lint-changed: nothing differs from $base"
  exit 0
fi

lint_pkgs="$(printf '%s\n' "$paths" | python3 -c '
import sys
sys.path.insert(0, "scripts/ci")
import affected_packages as ap
_, changed, _, _ = ap.select(sys.stdin.read().splitlines(), ap.go_packages())
print(" ".join("./" + d if d != "." else "." for d in changed))
')"

echo "lint-changed: $(printf '%s\n' "$paths" | wc -l | tr -d ' ') changed files against $(git rev-parse --short "$base")"
echo "  lint ($(wc -w <<<"$lint_pkgs" | tr -d ' ')): ${lint_pkgs:-<none>}"
[[ "${DRY_RUN:-}" == 1 ]] && exit 0

failed=()
for pkg in $lint_pkgs; do
  echo "Checking $pkg"
  "$go_bin" tool golangci-lint run --allow-serial-runners --timeout=20m "$pkg" || failed+=("$pkg")
done
"$go_bin" run ./scripts/ci/teststorecheck -base "$base" || failed+=(teststorecheck)
"$go_bin" run ./scripts/ci/sealdomaincheck || failed+=(sealdomaincheck)
bash scripts/ci/check_trimpath_safe.sh "$repo_root" || failed+=(trimpath-check)
if ((${#failed[@]})); then
  echo "lint-changed: failed: ${failed[*]}" >&2
  exit 1
fi
