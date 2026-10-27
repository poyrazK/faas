#!/usr/bin/env bash
# Vet and test only what a change affects, with ci-light's selection
# (scripts/ci/affected_packages.py): vet the changed packages, then test them,
# the large split packages among them, and the §6.2 invariant tests when their
# imports changed. ci-light also vets every direct importer; VET=importers does
# the same here (it can be ~200 packages and many minutes when pkg/state or
# pkg/api changes).
#
# The change is everything that differs from the merge base with origin/main:
# commits, uncommitted edits and untracked files. Environment:
#   BASE=<ref>    compare against <ref> instead of the merge base
#   RACE=1        add -race to the tests
#   VET=importers vet the changed packages and their direct importers, as ci-light
#   VET=0         skip the vet step
#   GOFLAGS, LDFLAGS, GOMEMLIMIT  set by `make test-changed` (as `make test`)
#   DRY_RUN=1     print the selection without running anything
# Arguments are build flags for both vet and test. `make test-changed` passes
# none: it sets GOFLAGS to the Makefile's GO_DEV_GOFLAGS (as `make test`).
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"
go_bin="${GO:-go}"

base="${BASE:-$(git merge-base HEAD origin/main)}"
paths="$({ git diff --name-only "$base"; git ls-files --others --exclude-standard; } | sort -u)"
if [[ -z "$paths" ]]; then
  echo "test-changed: nothing differs from $base"
  exit 0
fi

# One `go list` pass: line 1 the changed packages, line 2 those plus their
# direct importers, line 3 the full test set (split packages included, unsharded).
selection="$(printf '%s\n' "$paths" | python3 -c '
import sys
sys.path.insert(0, "scripts/ci")
import affected_packages as ap
_, changed, importers, test = ap.select(sys.stdin.read().splitlines(), ap.go_packages())
fmt = lambda dirs: " ".join("./" + d if d != "." else "." for d in dirs)
print(fmt(changed))
print(fmt(importers))
print(fmt(test))
')"
vet_pkgs="$(sed -n 1p <<<"$selection")"
[[ "${VET:-}" == importers ]] && vet_pkgs="$(sed -n 2p <<<"$selection")"
test_pkgs="$(sed -n 3p <<<"$selection")"

echo "test-changed: $(printf '%s\n' "$paths" | wc -l | tr -d ' ') changed files against $(git rev-parse --short "$base")"
echo "  vet  ($(wc -w <<<"$vet_pkgs" | tr -d ' ')): ${vet_pkgs:-<none>}"
echo "  test ($(wc -w <<<"$test_pkgs" | tr -d ' ')): ${test_pkgs:-<none>}"
[[ "${DRY_RUN:-}" == 1 ]] && exit 0

build_flags=("$@")

if [[ "${VET:-1}" != 0 && -n "$vet_pkgs" ]]; then
  # shellcheck disable=SC2086 # space-separated package list
  "$go_bin" vet "${build_flags[@]}" $vet_pkgs
fi
if [[ -n "$test_pkgs" ]]; then
  test_flags=(-count=1)
  [[ -n "${LDFLAGS:-}" ]] && test_flags+=("$LDFLAGS")
  [[ "${RACE:-}" == 1 ]] && test_flags+=(-race)
  # shellcheck disable=SC2086 # space-separated package list
  "$go_bin" test "${build_flags[@]}" "${test_flags[@]}" $test_pkgs
fi
