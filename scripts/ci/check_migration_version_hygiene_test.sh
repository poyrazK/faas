#!/usr/bin/env bash
# Hermetic tests for check_migration_version_hygiene.sh. No GitHub API is
# contacted: the collision half needs credentials and is exercised in CI,
# so these cover the generator-shape half and the plumbing around it.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
checker="${repo_root}/scripts/ci/check_migration_version_hygiene.sh"
test_root="$(mktemp -d)"
trap 'rm -rf "${test_root}"' EXIT

git_init() {
  local dir="$1"
  mkdir -p "${dir}/migrations"
  git -C "${dir}" init -q -b main
  git -C "${dir}" config user.email test@example.invalid
  git -C "${dir}" config user.name "Migration version gate test"
  printf 'base\n' > "${dir}/README.md"
  git -C "${dir}" add .
  git -C "${dir}" commit -q -m 'chore: baseline'
}

# add_migration <dir> <version> — commit one added migration on a branch.
add_migration() {
  local dir="$1" version="$2"
  printf -- '-- +goose Up\nSELECT 1;\n' > "${dir}/migrations/${version}_thing.sql"
  git -C "${dir}" add "migrations/${version}_thing.sql"
  git -C "${dir}" commit -q -m "feat: add ${version}"
}

make_event() {
  local dir="$1"
  cat > "${dir}/event.json" <<EOF_EVENT
{"pull_request":{"number":7,"base":{"ref":"main","sha":"$(git -C "${dir}" rev-parse main)"},"head":{"ref":"t","sha":"$(git -C "${dir}" rev-parse HEAD)"}}}
EOF_EVENT
}

run_check() {
  local dir="$1"
  # No GITHUB_TOKEN: the open-pull-request half self-skips, which is the
  # documented local behaviour.
  (cd "${dir}" && GITHUB_EVENT_NAME=pull_request GITHUB_EVENT_PATH="${dir}/event.json" \
    GITHUB_REPOSITORY='' GITHUB_TOKEN='' bash "${checker}")
}

expect_pass() {
  local name="$1" dir="$2"
  if ! run_check "${dir}" >"${dir}/out" 2>&1; then
    echo "FAIL: ${name} unexpectedly failed" >&2; cat "${dir}/out" >&2; exit 1
  fi
}

expect_fail() {
  local name="$1" dir="$2" needle="$3"
  if run_check "${dir}" >"${dir}/out" 2>&1; then
    echo "FAIL: ${name} unexpectedly passed" >&2; cat "${dir}/out" >&2; exit 1
  fi
  grep -Fq "${needle}" "${dir}/out" || {
    echo "FAIL: ${name} failed for the wrong reason (wanted ${needle})" >&2
    cat "${dir}/out" >&2; exit 1
  }
}

# A generator-shaped version (non-zero milliseconds) is accepted.
generated="${test_root}/generated"
git_init "${generated}"
git -C "${generated}" checkout -q -b feature
add_migration "${generated}" 20260909143012487
make_event "${generated}"
expect_pass 'generator-shaped version' "${generated}"

# A hand-typed round timestamp is rejected. This is the exact shape of
# every collision that has broken main.
typed="${test_root}/typed"
git_init "${typed}"
git -C "${typed}" checkout -q -b feature
add_migration "${typed}" 20260909160000000
make_event "${typed}"
expect_fail 'hand-typed round timestamp' "${typed}" 'end in 000 milliseconds'

# Legacy 1..590 versions predate the timestamp scheme and must not trip it.
legacy="${test_root}/legacy"
git_init "${legacy}"
git -C "${legacy}" checkout -q -b feature
add_migration "${legacy}" 00591
make_event "${legacy}"
expect_pass 'legacy numbered version' "${legacy}"

# A pull request that adds no migrations passes without touching the API.
none="${test_root}/none"
git_init "${none}"
git -C "${none}" checkout -q -b feature
printf 'changed\n' > "${none}/README.md"
git -C "${none}" add README.md
git -C "${none}" commit -q -m 'docs: touch'
make_event "${none}"
expect_pass 'no migrations added' "${none}"

# Modifying an existing migration is not "adding" one — the diff filter is
# ACMRTUXB-free on purpose, so a rename or edit must not trip the gate.
edited="${test_root}/edited"
git_init "${edited}"
add_migration "${edited}" 20260909160000000   # lands on main, pre-existing
git -C "${edited}" checkout -q -b feature
printf -- '-- +goose Up\nSELECT 2;\n' > "${edited}/migrations/20260909160000000_thing.sql"
git -C "${edited}" add migrations/20260909160000000_thing.sql
git -C "${edited}" commit -q -m 'fix: edit existing migration'
make_event "${edited}"
expect_pass 'editing an existing migration' "${edited}"

echo "check_migration_version_hygiene: OK"
