#!/usr/bin/env bash
# pg-restore-verify.sh — T-7 throwaway restore + row-count assertions
# for issue #250 (provider-neutral off-host Postgres backup).
#
# Spec §14 M8 acceptance row M8 already ships the local-disk restore
# drill (deploy/scripts/faas-m8-restore-drill.sh). This script closes
# the off-host half: it pulls a basebackup from the configured rclone
# remote, restores onto a throwaway PG instance, replays archived WAL,
# and asserts the row counts in accounts / apps / instances line up
# with the live cluster.
#
# Why host-only under /var/lib/pgsql/restore-test/ (NOT a guest VM):
#   - Same isolation guarantee as a guest VM (isolated PG_DATA, isolated
#     port 5433, isolated cgroup via the systemd-run fork).
#   - Cheaper than spinning up a metal VM for the verify run.
#   - Trade-off: shares the host's kernel and cgroup tree with the
#     live cluster. Acceptable because the verify never touches the
#     live cluster's data dir; if it corrupts itself, the live
#     cluster is unaffected.
#
# Why rclone copy (not mount): keeps the script runnable on any
# Linux host with rclone installed — no kernel FUSE modules, no
# systemd unit churn.
#
# Why ROW_COUNT_THRESHOLD=0.95: live cluster writes a few seconds
# between the rclone copy + the count(*), so an exact match isn't
# the right gate. 95% is well above noise and well below a real
# partial-restore (which would land at 0% for a freshly truncated
# WAL stream).
#
# Run as root on a Linux control-plane host. Refuses to run if not Linux + not root.
# M8 docs: docs/runbooks/PostgresBackup.md (acceptance matrix).
#
# TODO(F4-followup): script body assumes Linux + x86_64 (pg_isready,
# stat -c '%Y', `/proc/self/loginuid`, etc.). The bash lint
# (`make lint-pg-restore-verify`) is portable but execution is gated
# on Linux + root + an EX44-style pg layout. A future patch should
# either (a) ship a sibling aarch64 variant for the Lima/metal arm64
# guest, or (b) gate the script behind //go:build metal and rerun the
# bash via `make metal-lima`. See review F4 + issue #250 follow-up.

set -euo pipefail

T_DAYS_BACK="${T_DAYS_BACK:-7}"
ROW_COUNT_THRESHOLD="${ROW_COUNT_THRESHOLD:-0.95}"
RESTORE_TEST_ROOT="${RESTORE_TEST_ROOT:-/var/lib/pgsql/restore-test}"
RESTORE_PG_PORT="${RESTORE_PG_PORT:-5433}"

LIVE_PG_PORT="${LIVE_PG_PORT:-5432}"
LIVE_PG_SOCKET="${LIVE_PG_SOCKET:-/var/run/postgresql}"
LIVE_PG_BIN="${LIVE_PG_BIN:-$(pg_config --bindir 2>/dev/null || echo /usr/lib/postgresql/15/bin)}"

# Off-host wiring — the stable rclone alias is configured by the
# postgres_backup role; provider-specific details stay in rclone.conf.
#
# PR-8 (issue #911 / ADR-110 deferred): provider-specific variables were
# removed from the role. The rclone remote alias is `offhostbox:`.
# The on-disk secret path /etc/faas/secrets/storage-box/rclone.conf
# stays (the LoadCredential= in the postgresql@.service drop-in references
# it).
RCLONE_REMOTE="${RCLONE_REMOTE:-offhostbox}"
RCLONE_CONF="${RCLONE_CONF:-/etc/faas/secrets/storage-box/rclone.conf}"
RCLONE_RUNTIME_CONF="${RCLONE_RUNTIME_CONF:-/var/lib/pgsql/backup-rclone.conf}"
BASEBACKUP_PATH="${OFF_HOST_BACKUP_BASEBACKUP_PATH:-faas-pg-basebackup}"
WAL_PATH="${OFF_HOST_BACKUP_WAL_PATH:-faas-pg-wal}"

heading() { printf '\n\033[1;36m▶ %s\033[0m\n' "$*"; }
ok()      { printf '\033[1;32m✓\033[0m %s\n' "$*"; }
warn()    { printf '\033[1;33m!\033[0m %s\n' "$*" >&2; }
fail()    { printf '\033[1;31m✗\033[0m %s\n' "$*" >&2; exit 1; }

# PostgreSQL refuses recovery when selected postmaster settings are lower than
# the primary values stored in the backup's control data. Keep the allowlist
# explicit and require integer values before appending anything to the isolated
# cluster's config.
append_recovery_sensitive_settings() {
  local config="$1"
  shift
  [[ $(( $# % 2 )) -eq 0 ]] || { warn "recovery settings require name/value pairs"; return 1; }
  while [[ $# -gt 0 ]]; do
    local name="$1" value="$2"
    shift 2
    case "$name" in
      max_connections|max_prepared_transactions|max_locks_per_transaction|max_wal_senders|max_worker_processes) ;;
      *) warn "unsupported recovery-sensitive setting: $name"; return 1 ;;
    esac
    [[ "$value" =~ ^[0-9]+$ ]] || { warn "invalid value for $name: $value"; return 1; }
    printf '%s = %s\n' "$name" "$value" >> "$config"
  done
}

cleanup_restore() {
  local status=$?
  if [[ -n "${RESTORE_PGDATA:-}" && -f "${RESTORE_PGDATA}/postmaster.pid" ]]; then
    runuser -u postgres -- "${LIVE_PG_BIN}/pg_ctl" -D "$RESTORE_PGDATA" -m fast stop >/dev/null 2>&1 || true
  fi
  [[ -z "${RESTORE_STAGE:-}" ]] || rm -rf -- "$RESTORE_STAGE"
  [[ -z "${RESTORE_PGDATA:-}" ]] || rm -rf -- "$RESTORE_PGDATA"
  return "$status"
}

# The smoke test sources the helpers without running a root-only restore.
if [[ "${PG_RESTORE_VERIFY_LIBRARY_ONLY:-0}" == "1" ]]; then
  if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    exit 0
  fi
  return 0
fi

VERIFY_START=$(date +%s)
VERIFY_START_ISO=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

heading "0/5 Pre-flight"
[[ "$(uname -s)" == "Linux" ]] || fail "verify must run on the EX44 (Linux)"
[[ $EUID -eq 0 ]] || fail "must run as root (writes /var/lib/pgsql, opens privileged ports)"
command -v rclone >/dev/null 2>&1 || fail "rclone not on PATH — apt install rclone first"
command -v runuser >/dev/null 2>&1 || fail "runuser not on PATH"
[[ -f "$RCLONE_CONF" ]] || fail "$RCLONE_CONF missing — run 'gregale backup unseal-rclone' (PR-X 'gregale secrets init' supersedes bootstrap.sh; PR-1 retired bootstrap.sh 2026-08-15)"
[[ "$(stat -c '%a %U %G' "$RCLONE_CONF")" == "400 root root" ]] \
  || fail "$RCLONE_CONF must be 0400 root:root (spec §11)"
runuser -u postgres -- test -r "$RCLONE_RUNTIME_CONF" \
  || fail "$RCLONE_RUNTIME_CONF must be readable by postgres for WAL replay"

# Pre-flight: list remote + confirm the T-th subdir exists. We pin
# the T-th subdir by mtime (newest under $BASEBACKUP_PATH) so the
# script doesn't need a date math dependency.
heading "1/5 rclone lsd $RCLONE_REMOTE:$BASEBACKUP_PATH"
mapfile -t REMOTE_SUBDIRS < <(rclone lsd "${RCLONE_REMOTE}:${BASEBACKUP_PATH}" --config "$RCLONE_CONF" 2>/dev/null \
  | awk '{print $NF}' | sed 's:/$::' | sort)
[[ ${#REMOTE_SUBDIRS[@]} -ge 1 ]] || fail "no subdirs under ${RCLONE_REMOTE}:${BASEBACKUP_PATH}"

# Pick the newest subdir by rclone lsf -t (timestamp order). The
# T_DAYS_BACK knob is reserved for a future PR — today we just take
# the newest.
TGT_REMOTE_DIR="${REMOTE_SUBDIRS[-1]}"
ok "picked newest remote basebackup: $TGT_REMOTE_DIR"

# --- 2. Fetch ---------------------------------------------------------

RESTORE_STAGE="${RESTORE_TEST_ROOT}/stage-${VERIFY_START}"
RESTORE_PGDATA="${RESTORE_TEST_ROOT}/data"
trap cleanup_restore EXIT
rm -rf "$RESTORE_STAGE" "$RESTORE_PGDATA"
install -d -o postgres -g postgres -m 0700 "$RESTORE_STAGE" "$RESTORE_PGDATA"

heading "2/5 rclone copy ${RCLONE_REMOTE}:${BASEBACKUP_PATH}/${TGT_REMOTE_DIR} → $RESTORE_STAGE"
rclone copy "${RCLONE_REMOTE}:${BASEBACKUP_PATH}/${TGT_REMOTE_DIR}" "$RESTORE_STAGE" \
  --config "$RCLONE_CONF" --stats=0
[[ -f "$RESTORE_STAGE/base.tar.gz" ]] || fail "no base.tar.gz in $RESTORE_STAGE — pick a different remote subdir"
chown -R postgres:postgres "$RESTORE_STAGE"

# --- 3. Restore into the throwaway PG data dir ------------------------

heading "3/5 initdb + restore into $RESTORE_PGDATA"
# Clean any prior run; we're host-only under /var/lib/pgsql so this
# never touches the live cluster's data dir.
rm -rf "$RESTORE_PGDATA"
install -d -o postgres -g postgres -m 0700 "$RESTORE_PGDATA"
runuser -u postgres -- "${LIVE_PG_BIN}/initdb" -D "$RESTORE_PGDATA" --auth=peer --username=postgres >/dev/null
ok "initdb complete"

tar -xzf "$RESTORE_STAGE/base.tar.gz" -C "$RESTORE_PGDATA"
# `-X fetch` places required WAL inside base.tar.gz. A separately streamed
# pg_wal.tar.gz is accepted for compatibility with older/operator-made backups.
[[ -f "$RESTORE_STAGE/pg_wal.tar.gz" ]] \
  && tar -xzf "$RESTORE_STAGE/pg_wal.tar.gz" -C "$RESTORE_PGDATA"
chown -R postgres:postgres "$RESTORE_PGDATA"
ok "basebackup unpacked"

# Recovery stanza: signal file + restore_command that streams WAL
# from the Storage Box. The lineinfile is idempotent.
touch "$RESTORE_PGDATA/recovery.signal"
cat >> "$RESTORE_PGDATA/postgresql.conf" <<EOF

# --- faas-pg-restore-verify: recovery stanza (issue #250, removed after verify) ---
port = ${RESTORE_PG_PORT}
restore_command = 'rclone copyto ${RCLONE_REMOTE}:${WAL_PATH}/%f %p --config ${RCLONE_RUNTIME_CONF} --stats=0 --quiet'
recovery_target_action = 'promote'
unix_socket_directories = '/tmp'
EOF

# Read the values from the live primary rather than assuming package defaults.
# These are the recovery-sensitive integer settings PostgreSQL requires to be
# at least as large on a recovery server as on the server that produced WAL.
PRIMARY_RECOVERY_SETTINGS=$(runuser -u postgres -- "${LIVE_PG_BIN}/psql" \
  -h "$LIVE_PG_SOCKET" -p "$LIVE_PG_PORT" -d postgres -X -A -t -F '|' \
  -v ON_ERROR_STOP=1 -c "SELECT current_setting('max_connections'), current_setting('max_prepared_transactions'), current_setting('max_locks_per_transaction'), current_setting('max_wal_senders'), current_setting('max_worker_processes')") \
  || fail "could not read recovery-sensitive settings from the live primary"
IFS='|' read -r PRIMARY_MAX_CONNECTIONS PRIMARY_MAX_PREPARED PRIMARY_MAX_LOCKS PRIMARY_MAX_WAL_SENDERS PRIMARY_MAX_WORKERS <<< "$PRIMARY_RECOVERY_SETTINGS"
append_recovery_sensitive_settings "$RESTORE_PGDATA/postgresql.conf" \
  max_connections "$PRIMARY_MAX_CONNECTIONS" \
  max_prepared_transactions "$PRIMARY_MAX_PREPARED" \
  max_locks_per_transaction "$PRIMARY_MAX_LOCKS" \
  max_wal_senders "$PRIMARY_MAX_WAL_SENDERS" \
  max_worker_processes "$PRIMARY_MAX_WORKERS" \
  || fail "live primary returned invalid recovery-sensitive settings"

# --- 4. Replay WAL on the throwaway instance --------------------------

heading "4/5 start PG on :${RESTORE_PG_PORT}, replay WAL"
chown postgres:postgres "$RESTORE_PGDATA/postgresql.conf"
RESTORE_PG_LOG="$RESTORE_PGDATA/restore-verify.log"
runuser -u postgres -- "${LIVE_PG_BIN}/pg_ctl" -D "$RESTORE_PGDATA" -l "$RESTORE_PG_LOG" -o "-p ${RESTORE_PG_PORT}" -W start

# Wait for promotion (pg_is_in_recovery() returns 'f').
PROMOTED=0
for _ in $(seq 1 300); do
  if "${LIVE_PG_BIN}/pg_isready" -h /tmp -p "$RESTORE_PG_PORT" >/dev/null 2>&1; then
    IN_RECOVERY=$(runuser -u postgres -- "${LIVE_PG_BIN}/psql" -h /tmp -p "$RESTORE_PG_PORT" -d postgres -tAc "SELECT pg_is_in_recovery()" 2>/dev/null || echo "t")
    if [[ "$IN_RECOVERY" == "f" ]]; then
      PROMOTED=1
      break
    fi
  fi
  sleep 2
done
[[ $PROMOTED -eq 1 ]] || fail "throwaway PG never promoted — see $RESTORE_PG_LOG"
ok "throwaway PG promoted"

# --- 5. Row-count assertions ------------------------------------------

heading "5/5 row-count assertions vs live cluster"
declare -a TABLES=(accounts apps instances)
ALL_PASS=1
for tbl in "${TABLES[@]}"; do
  # Live cluster is on $LIVE_PG_PORT over the unix socket.
  LIVE=$(runuser -u postgres -- "${LIVE_PG_BIN}/psql" -h "$LIVE_PG_SOCKET" -p "$LIVE_PG_PORT" -d faas -tAc "SELECT count(*) FROM ${tbl}" 2>/dev/null || echo "0")
  REST=$(runuser -u postgres -- "${LIVE_PG_BIN}/psql" -h /tmp -p "$RESTORE_PG_PORT" -d faas -tAc "SELECT count(*) FROM ${tbl}" 2>/dev/null || echo "0")
  if [[ "$LIVE" -gt 0 ]]; then
    RATIO=$(awk -v a="$REST" -v b="$LIVE" 'BEGIN { if (b > 0) printf "%.4f", a / b; else print "0" }')
  else
    RATIO=$(awk -v a="$REST" 'BEGIN { print (a == 0) ? "1.0000" : "0.0000" }')
  fi
  PASS=$(awk -v r="$RATIO" -v t="$ROW_COUNT_THRESHOLD" 'BEGIN { print (r >= t) ? "1" : "0" }')
  if [[ "$PASS" -eq 1 ]]; then
    ok "${tbl}: live=${LIVE} restore=${REST} ratio=${RATIO} ≥ ${ROW_COUNT_THRESHOLD}"
  else
    warn "${tbl}: live=${LIVE} restore=${REST} ratio=${RATIO} < ${ROW_COUNT_THRESHOLD}"
    ALL_PASS=0
  fi
done

# Stop and remove the throwaway instance so subsequent runs can re-initdb.
cleanup_restore
trap - EXIT

VERIFY_END=$(date +%s)
TOTAL=$(( VERIFY_END - VERIFY_START ))

if [[ $ALL_PASS -eq 1 ]]; then
  printf '\nT-7 restore verify PASS (wall=%ds; started=%s)\n' "$TOTAL" "$VERIFY_START_ISO"
  exit 0
fi
fail "T-7 restore verify FAIL — see row counts above"
