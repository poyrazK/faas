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
cp cmd/gregale/templates/data-api/{config.mjs,access.mjs,server.mjs,types.mjs,type-render.mjs,refresh.mjs,refresh-rotation.mjs,logging.mjs,timing.mjs,database-timing.mjs,admission.mjs,health.mjs,pool.mjs,deadline.mjs,sql-deadline.mjs,response-limit.mjs,rate-limit.mjs,user-concurrency.mjs,connection-observations.mjs,usage.mjs,storage.mjs,dev.mjs,dev-project.mjs,dev-upgrade.mjs,dev-explain.mjs,dev-access.mjs,guided-access.mjs,guided-mutations.mjs,mutation-observer.mjs,fixture-builder.mjs,starter-verification.json,dev-inspector.mjs,dev-requests.mjs,request-replay.mjs,hosted-replay.mjs,inspector.html,inspector.mjs,inspector-data.mjs,csv-import.mjs,csv-export.mjs,bulk-write.mjs,bulk-history.mjs,package.json,package-lock.json} "$runtime_dir/"
cp cmd/gregale/templates/data-api-starter/migrations/{migrate.mjs,rpc-permissions.mjs} "$runtime_dir/"
mkdir -p "$runtime_dir/rpc-runtime"
cp cmd/gregale/templates/data-api/{types.mjs,type-render.mjs,config.mjs} "$runtime_dir/rpc-runtime/"
cp -R cmd/gregale/templates/data-api/test "$runtime_dir/"
npm ci --prefix "$runtime_dir" --ignore-scripts
npm test --prefix "$runtime_dir"
DATA_API_RUNTIME_DIR="$runtime_dir" node --test tests/data-api/connection-observations.test.mjs
support_console_dir="$runtime_dir/support-console"
cp -R cmd/gregale/templates/data-api-support-console "$support_console_dir"
cp cmd/gregale/templates/data-api/csv-export.mjs "$support_console_dir/public/csv.mjs"
npm ci --prefix "$support_console_dir" --ignore-scripts
npm test --prefix "$support_console_dir"
DATA_API_SUPPORT_CONSOLE_DIR="$support_console_dir" node --test tests/data-api/support-console.test.mjs
DATA_API_RUNTIME_DIR="$runtime_dir" node --test tests/data-api/dev-watch.test.mjs tests/data-api/dev-requests.test.mjs tests/data-api/dev-project.test.mjs tests/data-api/dev-upgrade.test.mjs tests/data-api/fixture-policy.test.mjs tests/data-api/explain.test.mjs tests/data-api/access.test.mjs tests/data-api/guided-access.test.mjs tests/data-api/guided-mutations.test.mjs tests/data-api/guided-lifecycle.test.mjs tests/data-api/hosted-replay.test.mjs
npm ci --prefix sdk/data --ignore-scripts
npm test --prefix sdk/data
node --test tests/data-api/replay-expiry-contract.test.mjs tests/data-api/mutation-journal-contract.test.mjs tests/data-api/receipt-diagnostics-contract.test.mjs tests/data-api/note-change-feed-contract.test.mjs tests/data-api/note-search-contract.test.mjs
DATA_API_RUNTIME_DIR="$runtime_dir" node --test tests/data-api/type-helpers.test.mjs tests/data-api/input-validators.test.mjs tests/data-api/schema-forms.test.mjs tests/data-api/relationships.test.mjs tests/data-api/csv-import.test.mjs tests/data-api/csv-export.test.mjs tests/data-api/bulk-write.test.mjs tests/data-api/bulk-history.test.mjs tests/data-api/read-only.test.mjs
starter_dir="$runtime_dir/starter"
cp -R cmd/gregale/templates/data-api-starter "$starter_dir"
mkdir -p "$starter_dir/migrations/rpc-runtime"
cp cmd/gregale/templates/data-api/{types.mjs,type-render.mjs,config.mjs} "$starter_dir/migrations/rpc-runtime/"
npm ci --prefix "$starter_dir" --ignore-scripts
package_file="$(cd sdk/data && npm pack --silent --pack-destination "$starter_dir")"
node "$starter_dir/tools/install-sdk.mjs" "$starter_dir/$package_file"
npm test --prefix "$starter_dir"
node --test tests/data-api/install-sdk.test.mjs
node --test tests/data-api/artifacts.test.mjs
node --test tests/data-api/browser-inspector.test.mjs tests/data-api/inspector-refresh.test.mjs tests/data-api/browser-cors.test.mjs cmd/gregale/templates/data-api-teams/workers/order-webhooks.test.mjs
node --test tests/data-api/staging/*.test.mjs
node --test tests/data-api/load/*.test.mjs
if [[ "${1:-}" == "--integration" ]]; then
  DATA_API_RUNTIME_DIR="$runtime_dir" node tests/data-api/load/verify.mjs --output "${DATA_API_LOAD_REPORT:-$runtime_dir/load-report.json}"
  DATA_API_LOAD_FAILURE_TESTS=1 DATA_API_RUNTIME_DIR="$runtime_dir" node --test tests/data-api/load/integration.test.mjs
  DATA_API_HOSTED_LOAD_SQL_TEST=1 DATA_API_RUNTIME_DIR="$runtime_dir" node --test tests/data-api/staging/load-postgres.test.mjs
  DATA_API_SUPPORT_CONSOLE_DIR="$support_console_dir" DATA_API_RUNTIME_DIR="$runtime_dir" node --test tests/data-api/support-console-postgres.test.mjs
  DATA_API_SUPPORT_CONSOLE_DIR="$support_console_dir" node --test tests/data-api/support-audit-postgres.test.mjs tests/data-api/support-actions-postgres.test.mjs
  DATA_API_RUNTIME_DIR="$runtime_dir" DATA_API_STARTER_DIR="$starter_dir" node --test tests/data-api/integration.test.mjs tests/data-api/storage.test.mjs tests/data-api/starter.test.mjs tests/data-api/note-search.test.mjs tests/data-api/admission.test.mjs tests/data-api/deadline.test.mjs tests/data-api/pool.test.mjs tests/data-api/lock-contention.test.mjs tests/data-api/sql-deadline.test.mjs tests/data-api/sql-deadline-enrollment.test.mjs tests/data-api/refresh-rotation.test.mjs
  go build -o "$runtime_dir/gregale-dev-diff" ./cmd/gregale
  DATA_API_DEV_CLI="$runtime_dir/gregale-dev-diff" DATA_API_RUNTIME_DIR="$runtime_dir" node --test tests/data-api/receipt-diagnostics.test.mjs
  DATA_API_DEV_CLI="$runtime_dir/gregale-dev-diff" DATA_API_RUNTIME_DIR="$runtime_dir" DATA_API_STARTER_DIR="$starter_dir" node --test tests/data-api/database-timing.test.mjs tests/data-api/diagnostics.test.mjs tests/data-api/health.test.mjs tests/data-api/response-limit.test.mjs tests/data-api/rate-limit.test.mjs tests/data-api/user-concurrency.test.mjs tests/data-api/composites.test.mjs tests/data-api/view-relationships.test.mjs tests/data-api/computed-relationships.test.mjs tests/data-api/json-projections.test.mjs tests/data-api/cursor-pagination.test.mjs tests/data-api/cancellation.test.mjs tests/data-api/validated-rpc.test.mjs tests/data-api/rpc-contract-drift.test.mjs tests/data-api/optimistic-concurrency.test.mjs tests/data-api/idempotency.test.mjs tests/data-api/transaction.test.mjs tests/data-api/transaction-v2.test.mjs tests/data-api/team-orders.test.mjs tests/data-api/team-order-summary.test.mjs tests/data-api/outbox.test.mjs tests/data-api/bulk-mutations.test.mjs tests/data-api/import-preview.test.mjs tests/data-api/coordinated-refresh.test.mjs
  DATA_API_DEV_CLI="$runtime_dir/gregale-dev-diff" DATA_API_RUNTIME_DIR="$runtime_dir" DATA_API_STARTER_DIR="$starter_dir" node --test tests/data-api/dev.test.mjs
  DATA_API_DEV_CLI="$runtime_dir/gregale-dev-diff" DATA_API_RUNTIME_DIR="$runtime_dir" DATA_API_STARTER_DIR="$starter_dir" node --test tests/data-api/fixture-builder.test.mjs
  DATA_API_DEV_CLI="$runtime_dir/gregale-dev-diff" DATA_API_RUNTIME_DIR="$runtime_dir" DATA_API_STARTER_DIR="$starter_dir" DATA_API_EXPLAIN_NATIVE=1 node --test tests/data-api/explain.test.mjs
  DATA_API_DEV_CLI="$runtime_dir/gregale-dev-diff" DATA_API_RUNTIME_DIR="$runtime_dir" DATA_API_STARTER_DIR="$starter_dir" DATA_API_VERIFY_NATIVE=1 node --test tests/data-api/verify.test.mjs
  DATA_API_DEV_CLI="$runtime_dir/gregale-dev-diff" DATA_API_RUNTIME_DIR="$runtime_dir" DATA_API_STARTER_DIR="$starter_dir" DATA_API_VERIFY_NATIVE=1 node --test tests/data-api/verify-init.test.mjs
  DATA_API_DEV_CLI="$runtime_dir/gregale-dev-diff" DATA_API_RUNTIME_DIR="$runtime_dir" DATA_API_STARTER_DIR="$starter_dir" DATA_API_VERIFY_NATIVE=1 node --test tests/data-api/release.test.mjs
fi
npm run typecheck --prefix "$starter_dir/client"
npm test --prefix "$starter_dir/client"
