#!/bin/sh
set -eu
: "${DATABASE_URL:?Set DATABASE_URL to a disposable PostgreSQL cluster with CREATEDB permission}"
if [ -n "${FAAS_SKIP_PG_TESTS:-}" ]; then
  echo 'Commit SDK acceptance cannot disable PostgreSQL tests.' >&2
  exit 1
fi
task_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
task_python=${GREGALE_COMMIT_PYTHON:-python3}
command -v npm >/dev/null || { echo 'npm is required for Node SDK acceptance.' >&2; exit 1; }
"$task_python" -c 'import psycopg, httpx, attrs' || {
  echo 'Install the Python SDK and sdk/commit-tests/requirements.txt in the selected interpreter.' >&2
  exit 1
}
task_results=$(mktemp "${TMPDIR:-/tmp}/gregale-commit-sdk.XXXXXX")
trap 'rm "$task_results"' EXIT HUP INT TERM
cd "$task_root/sdk/commit-tests/go"
if ! "${GO:-go}" test -p 1 -json -count=1 -run '^TestCommitSQLTransactionBoundary$' ./... > "$task_results"; then
  cat "$task_results"
  exit 1
fi
"$task_python" - "$task_results" <<'PY'
import json
import sys
if not any(event.get('Action') == 'pass' and event.get('Test') == 'TestCommitSQLTransactionBoundary'
           for event in map(json.loads, open(sys.argv[1]))):
    raise SystemExit('Go transaction acceptance did not pass; skipping is not qualification.')
PY
cd "$task_root/sdk/node"
npm run test:commit
cd "$task_root"
PYTHONPATH="$task_root/sdk/python${PYTHONPATH:+:$PYTHONPATH}" "$task_python" sdk/python/tests/test_commit_postgres.py
echo 'Go, Node, and Python customer transaction acceptance passed.'
