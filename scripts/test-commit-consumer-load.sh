#!/bin/sh
set -eu
: "${DATABASE_URL:?Set DATABASE_URL to a disposable PostgreSQL cluster with CREATEDB permission}"
: "${FAAS_COMMIT_TLS_PG_BIN_DIR:?Set FAAS_COMMIT_TLS_PG_BIN_DIR to the PostgreSQL server binary directory}"
if [ "$(uname -s)" != Linux ]; then
  echo 'Commit consumer load acceptance requires Linux daemon boot checks.' >&2
  exit 1
fi
if [ -n "${FAAS_SKIP_PG_TESTS:-}" ]; then
  echo 'Commit consumer load acceptance cannot disable PostgreSQL tests.' >&2
  exit 1
fi
for task_pg_tool in initdb pg_ctl; do
  if [ ! -x "$FAAS_COMMIT_TLS_PG_BIN_DIR/$task_pg_tool" ]; then
    echo "Missing PostgreSQL fixture executable: $task_pg_tool" >&2
    exit 1
  fi
done
if [ "$(id -u)" = 0 ]; then
  : "${FAAS_COMMIT_TLS_PG_USER:?Root acceptance requires an unprivileged PostgreSQL fixture user}"
  if [ "$(id -u "$FAAS_COMMIT_TLS_PG_USER")" = 0 ]; then
    echo 'The PostgreSQL fixture user must be unprivileged.' >&2
    exit 1
  fi
fi
task_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$task_root"
task_results=$(mktemp "${TMPDIR:-/tmp}/gregale-commit-consumer-load.XXXXXX")
trap 'rm -f "$task_results"' EXIT HUP INT TERM
# Receipt usage triggers reference public tables in the platform database.
export FAAS_PGTEST_TEMPLATE_DATABASE=1
task_case=sustained-load-and-recovery
task_timeout=20m
if [ -n "${GREGALE_COMMIT_LIVE_PROMETHEUS:-}" ]; then
  task_case=live-relay-outage-alerts
  task_timeout=25m
fi
if [ "${GREGALE_COMMIT_FLEET_DRILL:-}" = 1 ]; then
  : "${GREGALE_COMMIT_LIVE_PROMETHEUS:?The Commit fleet drill requires a Prometheus executable}"
  task_timeout=35m
fi
if [ "${GREGALE_COMMIT_FLEET_TAIL_TRACE:-}" = 1 ] || [ "${GREGALE_COMMIT_FLEET_SLOW_TLS_DRILL:-}" = 1 ]; then
  if [ "${GREGALE_COMMIT_FLEET_DRILL:-}" != 1 ]; then
    echo 'The Commit fleet timing drills require GREGALE_COMMIT_FLEET_DRILL=1' >&2
    exit 1
  fi
  task_timeout=45m
fi
if ! GREGALE_COMMIT_CONSUMER_LOAD_TEST=1 "${GO:-go}" test -p 1 -json -timeout "$task_timeout" -count=1 \
  -run '^(TestE2E_CommitHTTPConsumerCrashRecovery|TestCommitConsumerLoadProducerTransactions)$' ./cmd/e2e > "$task_results"; then
  cat "$task_results"
  exit 1
fi
python3 - "$task_results" "$task_case" <<'PY'
import json
import sys

required = {
    "TestE2E_CommitHTTPConsumerCrashRecovery",
    "TestE2E_CommitHTTPConsumerCrashRecovery/" + sys.argv[2],
    "TestCommitConsumerLoadProducerTransactions",
}
passed = set()
fragments = {}
for line in open(sys.argv[1], encoding="utf-8"):
    event = json.loads(line)
    if event.get("Action") in {"fail", "skip"}:
        raise SystemExit("Commit consumer load acceptance failed or skipped: " + str(event.get("Test", event.get("Package"))))
    if event.get("Action") == "pass" and event.get("Test") in required:
        passed.add(event["Test"])
    output = event.get("Output", "")
    if not output:
        continue
    key = (event.get("Package"), event.get("Test"))
    output = fragments.pop(key, "") + output
    # go test -json can split a long test log line across output events.
    if not output.endswith("\n"):
        fragments[key] = output
        continue
    if any(marker in output for marker in (
        "sustained Commit consumer load passed:",
        "Commit relay outage backlog drained:",
        "Commit live relay outage baseline ",
        "Commit recovery profile ",
        "Commit relay recovery trace ",
        "Commit fleet drill ",
        "Commit fleet tail trace ",
        "Commit fleet slow TLS ",
    )):
        print(output.strip())
if required - passed:
    raise SystemExit("Commit consumer load acceptance missing passing gates: " + ", ".join(sorted(required - passed)))
print("Commit consumer load and recovery acceptance passed. Native KVM and production qualification are separate.")
PY
