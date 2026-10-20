#!/usr/bin/env bash
# fetch_release_history.sh [--prefetch-deploy [REV]] — give a shallow,
# blobless checkout of a main commit the history the release planners read:
# main's history and the v* release tags, without file contents.
#
# The planners (affected_units.py, plan_unit_deploy.py, plan_host_config.py,
# plan_edge_rollout.py) run `git describe --tags`, `git merge-base
# --is-ancestor` and read trees of release commits. None reads another
# branch. A checkout with fetch-depth: 0 downloads every branch of the
# repository instead: about 4,000 branches and 1.66 GB, which took more than
# 13 minutes on the control-plane runner on 2026-10-20. Main and the tags
# without file contents are about 22 MB.
#
# Run it after actions/checkout with `filter: blob:none` (default depth 1).
#
# --prefetch-deploy [REV] also fetches, in one request, the contents of
# deploy/ at REV (default: the newest v* release tag before HEAD).
# host_roles.py reads those files with `git cat-file`, which in a partial
# clone fetches each missing file separately: 609 round trips at rc.283.
set -euo pipefail

remote="${FETCH_RELEASE_HISTORY_REMOTE:-origin}"
prefetch=false
rev=""
while (($#)); do
  case "$1" in
    --prefetch-deploy)
      prefetch=true
      if [[ $# -gt 1 && "$2" != --* ]]; then
        rev="$2"
        shift
      fi
      ;;
    *)
      echo "fetch_release_history.sh: unknown argument: $1" >&2
      exit 2
      ;;
  esac
  shift
done

unshallow=()
if [[ "$(git rev-parse --is-shallow-repository)" == true ]]; then
  unshallow=(--unshallow)
fi
git fetch --no-tags --filter=blob:none "${unshallow[@]}" "$remote" \
  '+refs/heads/main:refs/remotes/origin/main' \
  '+refs/tags/v*:refs/tags/v*'

if [[ "$prefetch" == true ]]; then
  if [[ -z "$rev" ]]; then
    rev="$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' HEAD)"
  fi
  list="$(mktemp)"
  trap 'rm -f "$list"' EXIT
  git ls-tree -r "$rev" -- deploy | awk '$2 == "blob" { print $3 }' | sort -u >"$list"
  if [[ -s "$list" ]]; then
    # The same request git makes for a missing object, for all of them at once.
    git -c fetch.negotiationAlgorithm=noop fetch --no-tags --no-write-fetch-head \
      --recurse-submodules=no --filter=blob:none --stdin "$remote" <"$list"
  fi
  echo "fetch_release_history: deploy/ at $rev prefetched ($(wc -l <"$list" | tr -d ' ') files)"
fi
