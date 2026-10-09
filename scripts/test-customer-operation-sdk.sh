#!/bin/sh
set -eu
: "${DATABASE_URL:?Set DATABASE_URL to a disposable PostgreSQL cluster with CREATEDB permission}"
if [ -n "${FAAS_SKIP_PG_TESTS:-}" ]; then
  echo 'Customer Operations receipt acceptance cannot disable PostgreSQL tests.' >&2
  exit 1
fi
task_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
task_python=${GREGALE_OPERATION_PYTHON:-python3}
task_results=$(mktemp "${TMPDIR:-/tmp}/gregale-customer-operation-sdk.XXXXXX")
trap 'rm "$task_results"' EXIT HUP INT TERM
cd "$task_root"
if ! GREGALE_CUSTOMER_OPERATION_SDK=1 "${GO:-go}" test -p 1 -race -json -count=1 -run '^Test(Mem|Pg)CustomerOperationCommittedReceipt$' ./pkg/operations/acceptance > "$task_results"; then
  cat "$task_results"
  exit 1
fi
"$task_python" - "$task_results" <<'PY'
import json
import sys
passed = {event.get('Test') for event in map(json.loads, open(sys.argv[1])) if event.get('Action') == 'pass'}
if not {'TestMemCustomerOperationCommittedReceipt', 'TestPgCustomerOperationCommittedReceipt'} <= passed:
    raise SystemExit('Customer Operations acceptance did not pass; skipping is not qualification.')
PY
echo 'Customer Operations committed-result replay and independent completion delivery acceptance passed.'
