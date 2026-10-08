#!/usr/bin/env bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
if [[ "${1:-}" == "--integration" ]]; then
  : "${DATA_API_TEST_DATABASE_URL:?A disposable administrative PostgreSQL URL is required}"
  : "${DATA_API_POSTGREST_BIN:?The pinned PostgREST executable is required}"
  "$DATA_API_POSTGREST_BIN" --version | grep -E '^PostgREST 14\.3([[:space:]]|$)' >/dev/null
fi
# Never install dependencies beneath go:embed templates: they would be shipped
# in the CLI/template archive. Test a temporary copy of the exact runtime source.
runtime_dir="$(mktemp -d "${TMPDIR:-/tmp}/gregale-data-api.XXXXXX")"
trap 'rm -rf "$runtime_dir"' EXIT
cp cmd/gregale/templates/data-api/{config.mjs,server.mjs,types.mjs,package.json,package-lock.json} "$runtime_dir/"
cp -R cmd/gregale/templates/data-api/test "$runtime_dir/"
npm ci --prefix "$runtime_dir" --ignore-scripts
npm test --prefix "$runtime_dir"
npm ci --prefix sdk/data --ignore-scripts
npm test --prefix sdk/data
node --test tests/data-api/staging/canary.test.mjs
if [[ "${1:-}" == "--integration" ]]; then
  DATA_API_RUNTIME_DIR="$runtime_dir" node --test tests/data-api/integration.test.mjs
fi
