#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
: "${DATABASE_URL:?test-issues requires reachable PostgreSQL DATABASE_URL}"
if [[ -n "${FAAS_SKIP_PG_TESTS:-}" ]]; then
  echo "test-issues refuses FAAS_SKIP_PG_TESTS" >&2
  exit 1
fi
command -v psql >/dev/null || { echo "test-issues requires psql on PATH" >&2; exit 1; }
command -v node >/dev/null
command -v python3 >/dev/null
PGCONNECT_TIMEOUT=5 psql "$DATABASE_URL" -X -v ON_ERROR_STOP=1 -Atc 'SELECT 1' >/dev/null
export FAAS_PGTEST_TEMPLATE_DATABASE=1 GREGALE_ISSUES_SDK_ACCEPTANCE=1
npm run build --prefix sdk/node
npm run test:build --prefix sdk/node
node --test sdk/node/dist-test/test/issues.test.js
PYTHONPATH=sdk/python python3 -m pytest -q sdk/python/tests/test_issues.py
FAAS_REPLAY_CHECK_VERSIONS=20260930100000001 go test -timeout=5m -ldflags='-s -w -linkmode=internal' -count=1 ./migrations -run '^TestNewMigrationsAreReplaySafe$'
go test -timeout=5m -ldflags='-s -w -linkmode=internal' -count=1 ./pkg/issues ./cmd/apid -run '^TestIssue'
go test -timeout=5m -ldflags='-s -w -linkmode=internal' -count=1 ./pkg/api ./pkg/productcap ./cmd/gregale ./pkg/dashboard
go run ./cmd/sdk-coverage
go run ./cmd/api-hosting-scorecard
echo 'Gregale Issues acceptance passed'
