#!/usr/bin/env bash
# Tests for light_diff_base.sh: the light tier's diff base for each event,
# including a merge-group base missing from a shallow checkout.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
script="$here/light_diff_base.sh"
tmp="$(mktemp -d)"
trap 'rm -rf -- "$tmp"' EXIT

export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@example.com GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@example.com
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1

fail() { echo "FAIL: $*" >&2; exit 1; }

# origin: main = c1 - c2 - c3; a merge-group branch c3 - g1 (the PR's squash).
git init -q -b main "$tmp/origin"
cd "$tmp/origin"
for n in 1 2 3; do echo "$n" > "f$n"; git add "f$n"; git commit -q -m "c$n"; done
c1="$(git rev-parse HEAD~2)" c2="$(git rev-parse HEAD~1)" c3="$(git rev-parse HEAD)"
git checkout -q -b gh-readonly-queue/main/pr-1
echo g > g; git add g; git commit -q -m g1
g1="$(git rev-parse HEAD)"
git checkout -q main
git config uploadpack.allowReachableSHA1InWant true

base() { # base EVENT [VAR=VALUE...]: run the script in the current checkout
  local event="$1"
  shift
  env EVENT_NAME="$event" BEFORE= MERGE_GROUP_BASE_SHA= "$@" bash "$script"
}

# pull_request: the merge commit's first parent.
git clone -q "file://$tmp/origin" "$tmp/pr"
cd "$tmp/pr"
git checkout -q -b merge "$c3"
echo pr > pr.txt; git add pr.txt; git commit -q -m pr-head
pr_head="$(git rev-parse HEAD)"
git checkout -q "$c3"
git merge -q --no-ff --no-edit "$pr_head"
[[ "$(base pull_request)" == "$c3" ]] || fail "pull_request base is not HEAD^1"

# push: BEFORE when it is a commit; HEAD~1 for a new branch (zeros) or none.
git clone -q "file://$tmp/origin" "$tmp/push"
cd "$tmp/push"
[[ "$(base push BEFORE="$c1")" == "$c1" ]] || fail "push base is not BEFORE"
[[ "$(base push BEFORE=0000000000000000000000000000000000000000)" == "$c2" ]] || fail "push from a new branch is not HEAD~1"
[[ "$(base push)" == "$c2" ]] || fail "push without BEFORE is not HEAD~1"
[[ "$(base workflow_dispatch BEFORE="$c1")" == "$c2" ]] || fail "a dispatch must not use BEFORE"

# push in a shallow checkout fetches BEFORE.
git clone -q --depth=2 "file://$tmp/origin" "$tmp/shallow-push"
cd "$tmp/shallow-push"
git cat-file -e "$c1^{commit}" 2>/dev/null && fail "fixture: c1 should be missing from the shallow clone"
[[ "$(base push BEFORE="$c1")" == "$c1" ]] || fail "shallow push did not fetch BEFORE"

# merge_group: the group's parent, fetched when the checkout lacks it.
git clone -q --depth=1 --branch gh-readonly-queue/main/pr-1 "file://$tmp/origin" "$tmp/queue"
cd "$tmp/queue"
[[ "$(git rev-parse HEAD)" == "$g1" ]] || fail "fixture: queue checkout is not the group head"
git cat-file -e "$c3^{commit}" 2>/dev/null && fail "fixture: base should be missing from the depth-1 clone"
[[ "$(base merge_group MERGE_GROUP_BASE_SHA="$c3")" == "$c3" ]] || fail "merge_group base is not the group's parent"
git diff --quiet "$(base merge_group MERGE_GROUP_BASE_SHA="$c3")" HEAD -- f1 f2 f3 || fail "merge_group diff touches main's files"
[[ "$(git diff --name-only "$c3" HEAD)" == g ]] || fail "merge_group diff is not the entry's own change"

# merge_group without a usable base fails instead of guessing.
if base merge_group >/dev/null 2>&1; then fail "merge_group without a base SHA passed"; fi
if base merge_group MERGE_GROUP_BASE_SHA=0123456789abcdef0123456789abcdef01234567 >/dev/null 2>&1; then
  fail "merge_group with an unknown base SHA passed"
fi

echo "light_diff_base_test: OK"
