#!/bin/sh
set -eu
: "${DATABASE_URL:?Set DATABASE_URL to a disposable PostgreSQL cluster with CREATEDB permission}"
if [ -n "${FAAS_SKIP_PG_TESTS:-}" ]; then
  echo 'Operation SDK acceptance cannot disable PostgreSQL tests.' >&2
  exit 1
fi
task_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
task_python=${GREGALE_OPERATION_PYTHON:-python3}
"$task_python" -c 'import psycopg, httpx, attrs'
task_results=$(mktemp "${TMPDIR:-/tmp}/gregale-operation-sdk.XXXXXX")
trap 'rm "$task_results"' EXIT HUP INT TERM
cd "$task_root"
"$task_python" scripts/gen-operation-inbox-schema.py --check
cd "$task_root/sdk/commit-tests/go"
if ! "${GO:-go}" test -p 1 -json -count=1 -run '^Test(Customer)?OperationSQLTransactionBoundary$' . > "$task_results"; then
  cat "$task_results"
  exit 1
fi
"$task_python" - "$task_results" <<'PY'
import json
import sys
passed = {event.get('Test') for event in map(json.loads, open(sys.argv[1])) if event.get('Action') == 'pass'}
if not {'TestOperationSQLTransactionBoundary', 'TestCustomerOperationSQLTransactionBoundary'} <= passed:
    raise SystemExit('Go operation acceptance did not pass; skipping is not qualification.')
PY
cd "$task_root/sdk/node"
npm run test:operations
cd "$task_root"
PYTHONPATH="$task_root/sdk/python${PYTHONPATH:+:$PYTHONPATH}" "$task_python" sdk/python/tests/test_operations_postgres.py
PYTHONPATH="$task_root/sdk/python${PYTHONPATH:+:$PYTHONPATH}" "$task_python" sdk/operation-tests/cross_sdk.py
echo 'Operation SDK transaction, process-death, and cross-language acceptance passed.'
