#!/bin/sh
set -eu
: "${DATABASE_URL:?Set DATABASE_URL to a disposable PostgreSQL cluster database}"
: "${FAAS_COMMIT_TLS_PG_BIN_DIR:?Set FAAS_COMMIT_TLS_PG_BIN_DIR to the PostgreSQL server binary directory}"
if [ "$(uname -s)" != Linux ]; then
  echo 'Commit process acceptance requires Linux daemon boot checks.' >&2
  exit 1
fi
if [ -n "${FAAS_SKIP_PG_TESTS:-}" ]; then
  echo 'Commit acceptance cannot run with PostgreSQL tests disabled.' >&2
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
  task_pg_uid=$(id -u "$FAAS_COMMIT_TLS_PG_USER")
  if [ "$task_pg_uid" = 0 ]; then
    echo 'The PostgreSQL fixture user must be unprivileged.' >&2
    exit 1
  fi
fi
task_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$task_root"
sh "$task_root/scripts/test-commit-sdk.sh"
task_results=$(mktemp "${TMPDIR:-/tmp}/gregale-commit-acceptance.XXXXXX")
trap 'rm -f "$task_results"' EXIT HUP INT TERM
if ! "${GO:-go}" test -p 1 -json -timeout 20m ./pkg/commit ./pkg/state ./cmd/apid ./cmd/e2e \
  -count=1 -run '^(TestConnectionCredentialSourceBindingAndRotation|TestHTTPAcceptorRefusesCredentialRedirect|TestPostgresRelay.*|TestPostgresConsumerDeduplication|TestPostgresTLSConnectionRecovery|TestPostgresSchemaQualificationRejectsMissingIdentity|TestPostgresSourceBindingRejectsAnotherDestination|TestPgCommitAcceptanceReplayConflictAndConcurrency|TestCommitPostgresToAPIHandoff|TestE2E_CommitProducerDeathReachesCompletedInvocation|TestE2E_CommitCLISourceLifecycle|TestE2E_CommitHTTPConsumerCrashRecovery)$' > "$task_results"; then
  cat "$task_results"
  exit 1
fi
python3 - "$task_results" <<'PY'
import json
import sys
required = {
    "TestConnectionCredentialSourceBindingAndRotation",
    "TestHTTPAcceptorRefusesCredentialRedirect",
    "TestPostgresSchemaQualificationRejectsMissingIdentity",
    "TestPostgresSourceBindingRejectsAnotherDestination",
    "TestPostgresConsumerDeduplication",
    "TestPostgresTLSConnectionRecovery",
    "TestPostgresRelayCommitRollbackAndLostAcceptance",
    "TestPostgresRelayBlockedReplayAndPendingCleanup",
    "TestPostgresRelayExpiredLeaseFencesOriginalWorker",
    "TestPgCommitAcceptanceReplayConflictAndConcurrency",
    "TestCommitPostgresToAPIHandoff",
    "TestE2E_CommitProducerDeathReachesCompletedInvocation",
    "TestE2E_CommitCLISourceLifecycle",
    "TestE2E_CommitHTTPConsumerCrashRecovery",
}
passed = set()
for line in open(sys.argv[1]):
    event = json.loads(line)
    if event.get("Action") == "pass" and event.get("Test") in required:
        passed.add(event["Test"])
missing = required - passed
if missing:
    raise SystemExit("Commit acceptance missing passing gates: " + ", ".join(sorted(missing)))
print("Commit PostgreSQL/process acceptance passed. Native KVM qualification is separate.")
PY
