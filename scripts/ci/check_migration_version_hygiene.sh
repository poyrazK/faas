#!/usr/bin/env bash
# Stop the migration-version collisions that keep breaking main.
#
# Two PRs merged 20260907160000000 on 2026-09-09 and every Postgres-backed
# job on main failed on `goose: duplicate version`. That was at least the
# fifth this week; main's history carries a run of "assign unique <x>
# version" and "remove stale collision copies" commits.
#
# The existing slot gate compares a PR against main AS IT IS WHEN THE GATE
# RUNS, so two PRs can each pass and still collide when both merge. A merge
# queue would fix the class, but GitHub's queue needs an organization-owned
# repository and this one is user-owned. So: two checks that need no
# GitHub feature.
#
#  1. A new timestamp version must not end in 000 milliseconds.
#     migrations.TimestampMigrationVersion is YYYYMMDDHHMMSS + 3 digits of
#     milliseconds, so a generator-produced version lands on .000 about one
#     time in a thousand. Every collision so far has been a hand-typed
#     round number like 16:00:00.000 — two people picking a round hour on
#     the same day collide by construction. The remedy is one command.
#
#  2. The version must not already be claimed by another OPEN pull
#     request. This is the check main actually lacked.
#
# Skips cleanly outside a pull request: on push to main the collision has
# already happened and `goose` reports it far more loudly than this could.

set -euo pipefail

event_name="${GITHUB_EVENT_NAME:-}"
if [[ "${event_name}" != "pull_request" ]]; then
  echo "migration-version-hygiene: skipped (event=${event_name:-local})"
  exit 0
fi

event_path="${GITHUB_EVENT_PATH:-}"
[[ -n "${event_path}" && -f "${event_path}" ]] || {
  echo "::error::migration-version-hygiene: GITHUB_EVENT_PATH is missing for a pull_request event" >&2
  exit 1
}
command -v jq >/dev/null 2>&1 || {
  echo "::error::migration-version-hygiene: jq is required" >&2
  exit 1
}

pr_number="$(jq -r '.pull_request.number // empty' "${event_path}")"
base_sha="$(jq -r '.pull_request.base.sha // empty' "${event_path}")"
head_sha="$(jq -r '.pull_request.head.sha // empty' "${event_path}")"
base_ref="$(jq -r '.pull_request.base.ref // empty' "${event_path}")"
[[ -n "${pr_number}" && -n "${base_sha}" && -n "${head_sha}" ]] || {
  echo "::error::migration-version-hygiene: pull request event is missing number/base/head" >&2
  exit 1
}

if ! git cat-file -e "${base_sha}^{commit}" 2>/dev/null; then
  [[ -n "${base_ref}" ]] || {
    echo "::error::migration-version-hygiene: base commit ${base_sha} unavailable and no base ref" >&2
    exit 1
  }
  git fetch --no-tags --quiet origin "refs/heads/${base_ref}:refs/remotes/origin/${base_ref}" || {
    echo "::error::migration-version-hygiene: could not fetch base ${base_ref}" >&2
    exit 1
  }
  git cat-file -e "${base_sha}^{commit}" 2>/dev/null || {
    echo "::error::migration-version-hygiene: base commit ${base_sha} still unavailable" >&2
    exit 1
  }
fi

merge_base="$(git merge-base "${base_sha}" "${head_sha}")" || {
  echo "::error::migration-version-hygiene: no merge base between ${base_sha} and ${head_sha}" >&2
  exit 1
}

# Capture into a variable: inside `while read < <(...)` a git failure is
# invisible to set -e and the check would silently pass on an empty list.
added="$(git diff --name-only --diff-filter=A "${merge_base}" "${head_sha}" -- 'migrations/*.sql')" || {
  echo "::error::migration-version-hygiene: git diff ${merge_base}..${head_sha} failed" >&2
  exit 1
}

mine=()
while IFS= read -r path; do
  [[ -n "${path}" ]] || continue
  version="$(sed -nE 's|.*/([0-9]+)_.*\.sql$|\1|p' <<<"${path}")"
  [[ -n "${version}" ]] || continue
  mine+=("${version}")
done <<<"${added}"

if ((${#mine[@]} == 0)); then
  echo "migration-version-hygiene: OK — this pull request adds no migrations"
  exit 0
fi

# ---- check 1: the version came from the generator ------------------------
# Legacy 1..590 versions predate the timestamp scheme; only apply this to
# the timestamp namespace.
min_timestamp_version=20260904000000000
bad_ms=()
for version in "${mine[@]}"; do
  ((10#${version} >= min_timestamp_version)) || continue
  [[ "${version: -3}" == "000" ]] && bad_ms+=("${version}")
done
if ((${#bad_ms[@]} > 0)); then
  echo "::error::migration-version-hygiene: version(s) end in 000 milliseconds, which means they were typed rather than generated" >&2
  printf '  %s\n' "${bad_ms[@]}" >&2
  echo "  Every collision that has broken main was a hand-typed round timestamp." >&2
  echo "  Run: make migration-new NAME=<lower_snake_case>   and move your SQL into the generated file." >&2
  exit 1
fi

# ---- check 2: nobody else has claimed it ---------------------------------
# The gate main lacked. Compares against every OTHER open pull request, so
# two in-flight branches cannot both pass and then collide on merge.
repo="${GITHUB_REPOSITORY:-}"
token="${GITHUB_TOKEN:-}"
if [[ -z "${repo}" || -z "${token}" ]]; then
  echo "migration-version-hygiene: no API credentials; skipped the open-pull-request collision check"
  echo "migration-version-hygiene: OK — generator-shaped versions: ${mine[*]}"
  exit 0
fi

api() {
  curl --fail --silent --show-error \
    -H "Authorization: Bearer ${token}" \
    -H "Accept: application/vnd.github+json" \
    "https://api.github.com/repos/${repo}/$1"
}

conflicts=0
open_prs="$(api "pulls?state=open&per_page=100" | jq -r '.[].number')" || {
  echo "migration-version-hygiene: could not list open pull requests; collision check skipped" >&2
  open_prs=""
}
for other in ${open_prs}; do
  [[ "${other}" != "${pr_number}" ]] || continue
  theirs="$(api "pulls/${other}/files?per_page=100" |
    jq -r '.[] | select(.status == "added") | .filename' |
    sed -nE 's|^migrations/([0-9]+)_.*\.sql$|\1|p')" || continue
  for version in "${mine[@]}"; do
    while IFS= read -r claimed; do
      [[ -n "${claimed}" ]] || continue
      if [[ "${claimed}" == "${version}" ]]; then
        echo "::error::migration-version-hygiene: version ${version} is already claimed by open pull request #${other}" >&2
        conflicts=1
      fi
    done <<<"${theirs}"
  done
done

if ((conflicts != 0)); then
  echo "  Both pull requests pass the slot gate today and break main the moment the second one merges." >&2
  echo "  Run: make migration-new NAME=<lower_snake_case>   to claim a fresh version." >&2
  exit 1
fi

echo "migration-version-hygiene: OK — ${#mine[@]} version(s) generator-shaped and unclaimed: ${mine[*]}"
