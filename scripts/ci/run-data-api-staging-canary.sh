#!/usr/bin/env bash
# Deploy customer fixture sources through Gregale's remote builder. Locally we
# compile the CLI and package the client SDK; customer images are never built here.
set -euo pipefail
umask 077
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root"
: "${FAAS_DATA_API_STAGING_CONFIG:?set the operator-owned staging manifest path}"
: "${FAAS_TOKEN:?configure the isolated staging account bearer in the secure environment}"
without_staging_credentials() (
  unset FAAS_TOKEN STAGING_CONFIG_JSON GREGALE_CONNECT_ALICE GREGALE_CONNECT_BOB
  unset GREGALE_DATA_API_ACCESS_TOKEN GREGALE_DATA_API_REFRESH_TOKEN GREGALE_ARTIFACT_TOKEN
  "$@"
)
export FAAS_DATA_API_SOURCE_COMMIT
FAAS_DATA_API_SOURCE_COMMIT="$(git rev-parse HEAD)"
node tests/data-api/staging/canary.mjs --check-config "$@"
git diff --quiet
git diff --cached --quiet
untracked_source="$(git ls-files --others --exclude-standard -- cmd/gregale sdk/data tests/data-api/staging tests/data-api/load scripts/ci/run-data-api-staging-canary.sh)"
[[ -z "$untracked_source" ]] || { echo 'Commit the canary and CLI sources before claiming source-commit evidence.' >&2; exit 1; }
UPDATE_DATA_API_RUNTIME_VERSION=0 without_staging_credentials go test -count=1 ./cmd/gregale/templates -run '^TestDataAPIRuntimeVersionMatchesItsFiles$'
export FAAS_DATA_API_EXPECTED_RUNTIME_VERSION
FAAS_DATA_API_EXPECTED_RUNTIME_VERSION="$(without_staging_credentials node -p 'require("./cmd/gregale/templates/data-api/runtime-version.json").version')"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/gregale-data-api-staging.XXXXXX")"
trap 'rm -rf -- "$work_dir"' EXIT
# Use one real, source-bound bundle for both the CLI and hosted SDK path.
without_staging_credentials node scripts/build-data-api-bundle.mjs --version v0.0.0-staging.0 --out-dir "$work_dir/bundle"
export FAAS_DATA_API_SDK_BUNDLE="$work_dir/bundle/data-api-bundle.json"
cli_filename="$(without_staging_credentials node --input-type=module -e 'import fs from "node:fs"; const b=JSON.parse(fs.readFileSync(process.argv[1],"utf8")); console.log(b.cli.archives[0].file)' "$FAAS_DATA_API_SDK_BUNDLE")"
without_staging_credentials tar -xzf "$work_dir/bundle/$cli_filename" -C "$work_dir"
without_staging_credentials npm ci --prefix sdk/data --ignore-scripts
export GREGALE_CANARY_BIN="$work_dir/gregale"
package_filename="$(without_staging_credentials node --input-type=module -e 'import fs from "node:fs"; const b=JSON.parse(fs.readFileSync(process.argv[1],"utf8")); console.log(b.sdk.file)' "$FAAS_DATA_API_SDK_BUNDLE")"
export FAAS_DATA_API_SDK_TARBALL="$work_dir/bundle/$package_filename"
# A fresh client's npm install needs dependency metadata as well as the
# tarballs fetched by npm ci. Resolve the bundled SDK once without credentials
# so the disposable hosted client can subsequently install entirely offline.
mkdir "$work_dir/cache-warm"
printf '%s\n' '{"private":true}' > "$work_dir/cache-warm/package.json"
without_staging_credentials npm install --prefix "$work_dir/cache-warm" --ignore-scripts --no-audit --no-fund "$FAAS_DATA_API_SDK_TARBALL"
node tests/data-api/staging/canary.mjs "$@"
