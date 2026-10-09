#!/usr/bin/env bash
# Deploy customer fixture sources through Gregale's remote builder. Locally we
# compile the CLI and package the client SDK; customer images are never built here.
set -euo pipefail
umask 077
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root"
: "${FAAS_DATA_API_STAGING_CONFIG:?set the operator-owned staging manifest path}"
: "${FAAS_TOKEN:?configure the isolated staging account bearer in the secure environment}"
export FAAS_DATA_API_SOURCE_COMMIT
FAAS_DATA_API_SOURCE_COMMIT="$(git rev-parse HEAD)"
node tests/data-api/staging/canary.mjs --check-config
git diff --quiet
git diff --cached --quiet
untracked_source="$(git ls-files --others --exclude-standard -- cmd/gregale sdk/data tests/data-api/staging scripts/ci/run-data-api-staging-canary.sh)"
[[ -z "$untracked_source" ]] || { echo 'Commit the canary and CLI sources before claiming source-commit evidence.' >&2; exit 1; }
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/gregale-data-api-staging.XXXXXX")"
trap 'rm -rf -- "$work_dir"' EXIT
go build -p 1 -o "$work_dir/gregale" ./cmd/gregale
npm ci --prefix sdk/data --ignore-scripts
npm run build --prefix sdk/data
(cd sdk/data && npm pack --pack-destination "$work_dir" --json > "$work_dir/package-receipt.json")
export GREGALE_CANARY_BIN="$work_dir/gregale"
package_filename="$(node --input-type=module -e 'import fs from "node:fs"; const [receipt] = JSON.parse(fs.readFileSync(process.argv[1], "utf8")); console.log(receipt.filename)' "$work_dir/package-receipt.json")"
export FAAS_DATA_API_SDK_TARBALL="$work_dir/$package_filename"
node tests/data-api/staging/canary.mjs
