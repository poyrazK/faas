#!/usr/bin/env bash
# light_diff_base.sh — print the commit the light tier diffs HEAD against
# (ci-light.yml; docs/adr/20261020214632-automatic-unit-deploy-safety-rails.md).
#
# The light tier lints and tests what a change touched, so every job needs
# the commit the change sits on:
#
#   pull_request  HEAD^1. The checkout is GitHub's merge commit, and its
#                 first parent is the base branch tip.
#   merge_group   MERGE_GROUP_BASE_SHA, the merge group's parent: the base
#                 branch with every entry ahead in the queue. Each entry is
#                 checked for its own change on top of what merges first.
#   push          BEFORE, the branch tip before the push, when it is a real
#                 commit; otherwise HEAD~1.
#   anything else HEAD~1.
#
# A base missing from a shallow checkout is fetched (one commit deep: the
# jobs diff two trees, which needs no common history). Inputs: EVENT_NAME,
# BEFORE, MERGE_GROUP_BASE_SHA; REMOTE (default origin) for the fetch.
set -euo pipefail

have_commit() { git cat-file -e "$1^{commit}" 2>/dev/null; }

# fetch_commit SHA: make SHA present; fails when the remote has no such commit.
fetch_commit() {
  have_commit "$1" && return 0
  git fetch -q --depth=1 "${REMOTE:-origin}" "$1" 2>/dev/null || true
  have_commit "$1"
}

is_sha() { [[ "$1" =~ ^[0-9a-f]{40}$ && ! "$1" =~ ^0+$ ]]; }

event="${EVENT_NAME:-}"
case "$event" in
  pull_request)
    git rev-parse HEAD^1
    ;;
  merge_group)
    base="${MERGE_GROUP_BASE_SHA:-}"
    is_sha "$base" || {
      echo "light_diff_base: merge_group event without a base SHA (MERGE_GROUP_BASE_SHA='$base')" >&2
      exit 1
    }
    fetch_commit "$base" || {
      echo "light_diff_base: merge group base $base is not fetchable" >&2
      exit 1
    }
    printf '%s\n' "$base"
    ;;
  *)
    before="${BEFORE:-}"
    if [[ "$event" == push ]] && is_sha "$before" && fetch_commit "$before"; then
      printf '%s\n' "$before"
    else
      git rev-parse HEAD~1
    fi
    ;;
esac
