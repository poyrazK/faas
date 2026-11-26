#!/bin/sh
set -eu
: "${FAAS_COMMIT_TLS_PG_BIN_DIR:?Set FAAS_COMMIT_TLS_PG_BIN_DIR to the PostgreSQL server binary directory}"
: "${FAAS_COMMIT_TLS_PG_USER:?Set FAAS_COMMIT_TLS_PG_USER to an unprivileged PostgreSQL fixture user}"
: "${DATABASE_URL:?Set DATABASE_URL to a disposable native acceptance PostgreSQL cluster}"
: "${FAAS_TEST_KERNEL:?Set FAAS_TEST_KERNEL to the qualified guest kernel}"
: "${FAAS_BUILDER_BASE_PATH:?Set FAAS_BUILDER_BASE_PATH to the qualified builder base}"
if [ "$(uname -s)" != Linux ] || [ "$(uname -m)" != x86_64 ] || [ ! -r /dev/kvm ] || [ "$(id -u)" != 0 ]; then
  echo 'Commit native acceptance requires a native x86_64 Linux KVM host with root.' >&2
  exit 1
fi
if [ -n "${FAAS_SKIP_PG_TESTS:-}" ]; then
  echo 'Commit native acceptance cannot disable PostgreSQL tests.' >&2
  exit 1
fi
for task_pg_tool in initdb pg_ctl; do
  if [ ! -x "$FAAS_COMMIT_TLS_PG_BIN_DIR/$task_pg_tool" ]; then
    echo "Missing PostgreSQL fixture executable: $task_pg_tool" >&2
    exit 1
  fi
done
task_pg_uid=$(id -u "$FAAS_COMMIT_TLS_PG_USER")
if [ "$task_pg_uid" = 0 ]; then
  echo 'The PostgreSQL fixture user must be unprivileged.' >&2
  exit 1
fi
for task_tool in firecracker jailer ip nft; do
  command -v "$task_tool" >/dev/null || { echo "Missing native dependency: $task_tool" >&2; exit 1; }
done
task_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$task_root"
# The disposable customer SQL server is reachable through a single test-only IP.
task_sql_ip=198.18.0.253
if [ -n "$(ip -4 address show to "$task_sql_ip/32")" ]; then
  echo 'Disposable Commit SQL proxy address already exists.' >&2
  exit 1
fi
task_results=$(mktemp "${TMPDIR:-/tmp}/gregale-commit-native.XXXXXX")
task_sql_owned=0
task_cleanup() {
  task_exit=$?
  trap - EXIT
  if [ "$task_sql_owned" = 1 ]; then
    ip address del "$task_sql_ip/32" dev lo || task_exit=1
  fi
  rm -f "$task_results" || task_exit=1
  exit "$task_exit"
}
trap task_cleanup EXIT
trap 'exit 1' HUP INT TERM
ip address add "$task_sql_ip/32" dev lo
task_sql_owned=1
export GREGALE_COMMIT_NATIVE_SQL_IP="$task_sql_ip"
task_status=0
"${GO:-go}" test -p 1 -tags metal -json ./cmd/e2e -count=1 -timeout 30m \
  -run '^TestSourceDeployWakeMetal$' > "$task_results" || task_status=$?
if [ -n "${FAAS_COMMIT_NATIVE_RESULTS:-}" ]; then
  if [ -e "$FAAS_COMMIT_NATIVE_RESULTS" ] || [ -L "$FAAS_COMMIT_NATIVE_RESULTS" ]; then
    echo 'Refusing to replace native Commit result evidence.' >&2
    exit 1
  fi
  (set -C; cat "$task_results" > "$FAAS_COMMIT_NATIVE_RESULTS")
fi
if [ "$task_status" -ne 0 ]; then
  cat "$task_results"
  exit "$task_status"
fi
python3 - "$task_results" <<'PY'
import json
import sys
required = {
    "TestSourceDeployWakeMetal",
    "TestSourceDeployWakeMetal/commit-snapshot-wake",
    "TestSourceDeployWakeMetal/commit-cold-boot-wake",
    "TestSourceDeployWakeMetal/commit-consumer-crash-recovery",
}
passed = set()
for line in open(sys.argv[1]):
    event = json.loads(line)
    if event.get("Action") in {"fail", "skip"}:
        raise SystemExit("Native Commit acceptance failed or skipped: " + str(event.get("Test", event.get("Package"))))
    if event.get("Action") == "pass" and event.get("Test") in required:
        passed.add(event["Test"])
if required - passed:
    raise SystemExit("Native Commit acceptance missing passes: " + ", ".join(sorted(required - passed)))
print("Native Commit producer and consumer crash recovery, snapshot restore, and cold boot passed.")
PY
