#!/usr/bin/env bash
# Smoke unit for the off-host Postgres restore-verify script. Asserts
# syntax + content-token presence in deploy/scripts/pg-restore-verify.sh
# (issue #250). Catches three regression classes:
#
#   1. Syntax breakage — `bash -n` without executing the script.
#   2. Required env-var / table-name drift — the table-list heredoc
#      and the rclone / RestoreTestRoot / ROW_COUNT_THRESHOLD knobs
#      must remain present in the script body. The same tokens are
#      pinned in the runbook (docs/runbooks/PostgresBackup.md) so a
#      silent rename breaks the on-call playbook.
#   3. offhostbox remote name drift — the script (and the ansible
#      postgres role's archive_command) refer to a stable remote
#      name; renaming breaks both at once.
#
# PR-8 (issue #911 / ADR-110 deferred): env vars renamed from
# HETZNER_STORAGE_BOX_* → OFF_HOST_BACKUP_*. Rclone remote alias
# hertznerbox: → offhostbox: (was misspelled before PR-8).
#
# Runs as part of `make lint-pg-restore-verify`. Exit 0 on success.

set -euo pipefail

SCRIPT="$(cd "$(dirname "$0")" && pwd)/pg-restore-verify.sh"

# 1. Syntax check on the verify script. Does NOT execute.
bash -n "$SCRIPT" || { echo "FAIL: bash -n $SCRIPT"; exit 1; }
echo "OK: bash -n"

# 2. Required table names + env-var names + threshold knob.
for tok in "accounts" "apps" "instances" \
           "ROW_COUNT_THRESHOLD" "T_DAYS_BACK" \
           "RESTORE_TEST_ROOT" "RESTORE_PG_PORT" \
           "OFF_HOST_BACKUP_BASEBACKUP_PATH" "OFF_HOST_BACKUP_WAL_PATH" \
           "rclone" "offhostbox" "pg_is_in_recovery" \
           "max_connections" "max_prepared_transactions" \
           "max_locks_per_transaction" "max_wal_senders" \
           "max_worker_processes" "trap cleanup_restore EXIT"; do
  grep -q "$tok" "$SCRIPT" || { echo "FAIL: missing token '$tok' in $SCRIPT"; exit 1; }
done
echo "OK: required tokens present in verify script"

# 3. Step headers present (so a refactor that drops a phase surfaces).
for hdr in "0/5 Pre-flight" "1/5 rclone lsd" "2/5 rclone copy" \
           "3/5 initdb" "4/5 start PG" "5/5 row-count assertions"; do
  grep -q "$hdr" "$SCRIPT" || { echo "FAIL: missing step header '$hdr'"; exit 1; }
done
echo "OK: step headers present"

# 4. Exercise the recovery-settings writer. This is the production failure
# mode from #2507: a primary max_connections above initdb's default must be
# written into the throwaway cluster before recovery starts.
# shellcheck disable=SC1090,SC1091
PG_RESTORE_VERIFY_LIBRARY_ONLY=1 source "$SCRIPT"
TEST_CONFIG=$(mktemp)
trap 'rm -f "$TEST_CONFIG"' EXIT
append_recovery_sensitive_settings "$TEST_CONFIG" \
  max_connections 160 \
  max_prepared_transactions 0 \
  max_locks_per_transaction 64 \
  max_wal_senders 10 \
  max_worker_processes 8
grep -qx 'max_connections = 160' "$TEST_CONFIG" \
  || { echo "FAIL: elevated primary max_connections not rendered"; exit 1; }
grep -qx 'max_worker_processes = 8' "$TEST_CONFIG" \
  || { echo "FAIL: complete recovery-sensitive setting set not rendered"; exit 1; }
if append_recovery_sensitive_settings "$TEST_CONFIG" shared_buffers 1024 2>/dev/null; then
  echo "FAIL: unsupported recovery setting accepted"
  exit 1
fi
echo "OK: recovery-sensitive primary settings rendered safely"
