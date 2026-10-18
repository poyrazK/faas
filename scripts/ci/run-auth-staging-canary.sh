#!/usr/bin/env bash
# ADR-1190 native acceptance: deploy the Auth app, a Data API bound to it and
# a migration release through Gregale's remote builder. Locally we compile only
# the CLI; customer images are never built here.
set -euo pipefail
umask 077
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root"
: "${FAAS_AUTH_STAGING_CONFIG:?set the operator-owned staging manifest path}"
: "${FAAS_TOKEN:?configure the isolated staging account bearer in the secure environment}"
export FAAS_AUTH_SOURCE_COMMIT
FAAS_AUTH_SOURCE_COMMIT="$(git rev-parse HEAD)"
node tests/auth/staging/canary.mjs --check-config
git diff --quiet
git diff --cached --quiet
untracked_source="$(git ls-files --others --exclude-standard -- cmd/gregale tests/auth/staging tests/data-api/staging scripts/ci/run-auth-staging-canary.sh)"
[[ -z "$untracked_source" ]] || { echo 'Commit the canary and CLI sources before claiming source-commit evidence.' >&2; exit 1; }
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/gregale-auth-staging.XXXXXX")"
trap 'rm -rf -- "$work_dir"' EXIT
go build -p 1 -o "$work_dir/gregale" ./cmd/gregale
export GREGALE_CANARY_BIN="$work_dir/gregale"
# Application Auth is dark-launched (ADR-1190): the CLI hides `gregale auth`
# unless this is set, and apid serves it only to accounts on
# FAAS_APPLICATION_AUTH_ACCOUNTS, which must include the staging account.
export FAAS_APPLICATION_AUTH_ENABLED=1
node tests/auth/staging/canary.mjs
