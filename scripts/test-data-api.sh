#!/usr/bin/env bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
if [[ "${1:-}" == "--integration" ]]; then
  : "${DATA_API_TEST_DATABASE_URL:?A disposable administrative PostgreSQL URL is required}"
  : "${DATA_API_POSTGREST_BIN:?The pinned PostgREST executable is required}"
  if [[ "${DATA_API_BROWSER_REQUIRED:-}" == "1" ]]; then
    : "${DATA_API_CHROMIUM_BIN:?A Chromium executable is required for browser acceptance}"
    [[ -x "$DATA_API_CHROMIUM_BIN" ]]
  fi
  "$DATA_API_POSTGREST_BIN" --version | grep -E '^PostgREST 14\.3([[:space:]]|$)' >/dev/null
fi
# Never install dependencies beneath go:embed templates: they would be shipped
# in the CLI/template archive. Test a temporary copy of the exact runtime source.
runtime_dir="$(mktemp -d "${TMPDIR:-/tmp}/gregale-data-api.XXXXXX")"
trap 'rm -rf "$runtime_dir"' EXIT
cp cmd/gregale/templates/data-api/{config.mjs,server.mjs,types.mjs,logging.mjs,dev.mjs,dev-inspector.mjs,dev-requests.mjs,request-replay.mjs,inspector.html,inspector.mjs,package.json,package-lock.json} "$runtime_dir/"
cp cmd/gregale/templates/data-api-starter/migrations/{migrate.mjs,rpc-permissions.mjs} "$runtime_dir/"
mkdir -p "$runtime_dir/rpc-runtime"
cp cmd/gregale/templates/data-api/{types.mjs,config.mjs} "$runtime_dir/rpc-runtime/"
cp -R cmd/gregale/templates/data-api/test "$runtime_dir/"
npm ci --prefix "$runtime_dir" --ignore-scripts
npm test --prefix "$runtime_dir"
DATA_API_RUNTIME_DIR="$runtime_dir" node --test tests/data-api/dev-watch.test.mjs tests/data-api/dev-requests.test.mjs
npm ci --prefix sdk/data --ignore-scripts
npm test --prefix sdk/data
starter_dir="$runtime_dir/starter"
cp -R cmd/gregale/templates/data-api-starter "$starter_dir"
mkdir -p "$starter_dir/migrations/rpc-runtime"
cp cmd/gregale/templates/data-api/{types.mjs,config.mjs} "$starter_dir/migrations/rpc-runtime/"
npm ci --prefix "$starter_dir" --ignore-scripts
package_file="$(cd sdk/data && npm pack --silent --pack-destination "$starter_dir")"
node "$starter_dir/tools/install-sdk.mjs" "$starter_dir/$package_file"
npm test --prefix "$starter_dir"
node --test tests/data-api/install-sdk.test.mjs
node --test tests/data-api/artifacts.test.mjs
node --test tests/data-api/browser-cors.test.mjs
node --test tests/data-api/staging/canary.test.mjs
if [[ "${1:-}" == "--integration" ]]; then
  DATA_API_RUNTIME_DIR="$runtime_dir" DATA_API_STARTER_DIR="$starter_dir" node --test tests/data-api/integration.test.mjs tests/data-api/starter.test.mjs
  go build -o "$runtime_dir/gregale-dev-diff" ./cmd/gregale
  DATA_API_DEV_CLI="$runtime_dir/gregale-dev-diff" DATA_API_RUNTIME_DIR="$runtime_dir" DATA_API_STARTER_DIR="$starter_dir" node --test tests/data-api/dev.test.mjs
fi
npm run typecheck --prefix "$starter_dir/client"
npm test --prefix "$starter_dir/client"
