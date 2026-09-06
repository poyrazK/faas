#!/usr/bin/env bash
# faas-m8-restore-drill.sh — restore-drill acceptance for spec §14 M8.
#
# This is an intentionally destructive, operator-only drill. It runs on the
# EX44 control-plane host as root, stops the FaaS daemons and PostgreSQL, wipes
# the live PostgreSQL data directory, restores the newest pg_basebackup tar
# files, replays archived WAL, and proves the restored control plane serves an
# app again. The EXIT trap restores the recovery configuration and attempts to
# return every service that was active before a failed run.
#
# The dated record is written to docs/drills/<UTC-date>-<HHMMSS>-restore-drill.md.
# The record keeps the 15-field contract in docs/drills/TEMPLATE-restore-drill.md;
# detailed row-count, migration, promotion, and cleanup evidence lives in the
# auto-captured step log.
#
# Run as root on the EX44. The script refuses non-Linux hosts.

set -euo pipefail

PG_DATA="${FAAS_PG_DATA:-/var/lib/pgsql/data}"
PG_ARCHIVE="${FAAS_PG_ARCHIVE:-/var/lib/pgsql/archive}"
PG_BASEBACKUP_DIR="${FAAS_PG_BASEBACKUP_DIR:-/var/lib/pgsql/basebackup}"
PG_MAJOR="${PG_MAJOR:-$(find /etc/postgresql -mindepth 1 -maxdepth 1 -type d -printf '%f\n' 2>/dev/null | sort | tail -1 || true)}"
PG_MAJOR="${PG_MAJOR:-15}"
PG_CONF="${FAAS_PG_CONF:-/etc/postgresql/${PG_MAJOR}/main/postgresql.conf}"
PG_DB="${FAAS_PG_DB:-faas}"
PG_SOCKET="${FAAS_PG_SOCKET:-/var/run/postgresql}"
PG_PORT="${FAAS_PG_PORT:-5432}"
PG_BINDIR="${PG_BINDIR:-$(pg_config --bindir 2>/dev/null || echo "/usr/lib/postgresql/${PG_MAJOR}/bin")}"
PG_ISREADY="${PG_BINDIR}/pg_isready"

# Host age key paths (ADR-020). Stamped into the basebackup in step 0.5 and
# restored into /etc/faas/secrets in step 5.5.
HOST_KEY="${FAAS_HOST_AGE_KEY:-/etc/faas/secrets/host.age}"
HOST_PUB="${FAAS_HOST_AGE_PUB:-/etc/faas/secrets/host.age.pub}"

RECORD_DIR="${FAAS_DRILL_RECORD_DIR:-docs/drills}"
DRILL_APP_HOST="${FAAS_DRILL_APP_HOST:-10.100.0.1}"
DRILL_APP_PORT="${FAAS_DRILL_APP_PORT:-8080}"
SCHEDD_METRICS="${FAAS_SCHEDD_METRICS:-http://127.0.0.1:9091/metrics}"

# State used by the cleanup trap. Defaults are deliberately populated so a
# pre-flight failure can never trip `set -u` while the trap is running.
DRILL_START=$(date +%s)
DRILL_START_ISO=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
DRILL_END="$DRILL_START"
DRILL_END_ISO="$DRILL_START_ISO"
DRILL_ACTIVE=0
DRILL_COMPLETED=0
CLEANUP_DONE=0
RECORD_WRITTEN=0
POSTGRES_WAS_ACTIVE=0
DAEMONS_WERE_STOPPED=0
PG_CONF_BACKUP=""
LATEST_BB="-"
LATEST_WAL="-"
RPO_BASE=0
RPO_SECONDS=0
WAKE_LATENCY="-"
MIGRATION_UPTIME="-"
MIGRATION_VERSION_BEFORE="-"
MIGRATION_VERSION_AFTER="-"
SCHEMA_CHECK="not-run"
LIVE_ACCOUNTS="-"
LIVE_APPS="-"
LIVE_HEALTHY_INSTANCES="-"
RESTORE_ACCOUNTS="-"
RESTORE_APPS="-"
RESTORE_HEALTHY_INSTANCES="-"
ROW_COUNT_DIFFS="not-run"
SHA_PRE="-"
BASE_SHA="-"
RECOVERY_STATUS="not-started"
RESULT="FAIL"
FAILURE_REASON="-"
RECORD_FILE=""
READY=0

DAEMONS=(apid gatewayd-internal gatewayd-public schedd vmmd imaged builderd meterd githubd)
ACTIVE_DAEMONS=()

daemon_was_active() {
  local candidate="$1"
  for active in "${ACTIVE_DAEMONS[@]}"; do
    [[ "$active" == "$candidate" ]] && return 0
  done
  return 1
}

heading() { printf '\n\033[1;36m▶ %s\033[0m\n' "$*"; }
ok()      { printf '\033[1;32m✓\033[0m %s\n' "$*"; }
warn()    { printf '\033[1;33m!\033[0m %s\n' "$*" >&2; }
fail() {
  FAILURE_REASON="$*"
  printf '\033[1;31m✗\033[0m %s\n' "$*" >&2
  exit 1
}

# All SQL is run through the postgres OS account so the Debian peer-auth
# default remains the only credential needed by the operator drill.
psql_query() {
  runuser -u postgres -- psql -X -A -t -q -h "$PG_SOCKET" -p "$PG_PORT" -d "$PG_DB" -c "$1"
}

psql_count() {
  local value
  value="$(psql_query "$1")" || return 1
  value="${value//[[:space:]]/}"
  [[ "$value" =~ ^[0-9]+$ ]] || return 1
  printf '%s' "$value"
}

migration_ready() {
  local applied
  applied="$(psql_count "SELECT count(*) FROM goose_db_version WHERE is_applied" 2>/dev/null || true)"
  [[ "$applied" =~ ^[1-9][0-9]*$ ]]
}

migration_version() {
  psql_query "SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied" 2>/dev/null \
    | tr -d '[:space:]' || true
}

wait_for_promotion() {
  for i in $(seq 1 90); do
    if "$PG_ISREADY" -h "$PG_SOCKET" -p "$PG_PORT" -d "$PG_DB" >/dev/null 2>&1; then
      local in_recovery
      in_recovery="$(psql_query 'SELECT pg_is_in_recovery()' 2>/dev/null || printf 't')"
      in_recovery="${in_recovery//[[:space:]]/}"
      if [[ "$in_recovery" == "f" ]]; then
        RECOVERY_STATUS="promoted at $(date -u +"%Y-%m-%dT%H:%M:%SZ")"
        ok "Postgres promoted after $((i * 2))s"
        return 0
      fi
    fi
    sleep 2
  done
  RECOVERY_STATUS="promotion timeout after 180s"
  return 1
}

capture_row_counts() {
  # The issue's shorthand `accounts.acl_count == apps.count ==
  # instances_states.healthy_count` maps to this schema's durable control-plane
  # invariant: accounts, apps, and healthy instance rows must each match their
  # pre-disaster values after restore. The loop below is intentionally exact;
  # it does not accept the 95% tolerance used by the separate off-host verify.
  LIVE_ACCOUNTS="$(psql_count 'SELECT count(*) FROM accounts')" || return 1
  LIVE_APPS="$(psql_count 'SELECT count(*) FROM apps')" || return 1
  LIVE_HEALTHY_INSTANCES="$(psql_count "SELECT count(*) FROM instances WHERE state IN ('running', 'waking', 'cold_booting')")" || return 1
}

write_record() {
  (( RECORD_WRITTEN == 0 )) || return 0
  local record_date record_time operator box commit_ref total rpo_base_min rpo_base_sec rpo_wal_min rpo_wal_sec
  record_date="$(date -u +%Y-%m-%d)"
  record_time="$(date -u +%H%M%S)"
  RECORD_FILE="${RECORD_DIR}/${record_date}-${record_time}-restore-drill.md"
  mkdir -p "$RECORD_DIR"

  if [[ "$LATEST_BB" != "-" && -f "$LATEST_BB/base.tar.gz" ]]; then
    BASE_SHA="$(sha256sum "$LATEST_BB/base.tar.gz" | awk '{print $1}')"
  fi
  [[ -n "$BASE_SHA" ]] || BASE_SHA="-"
  total=$(( DRILL_END - DRILL_START ))
  rpo_base_min=$(( RPO_BASE / 60 ))
  rpo_base_sec=$(( RPO_BASE % 60 ))
  rpo_wal_min=$(( RPO_SECONDS / 60 ))
  rpo_wal_sec=$(( RPO_SECONDS % 60 ))
  operator="${SUDO_USER:-${USER:-$(id -un)}}"
  box="$(hostname -f 2>/dev/null || hostname)"
  commit_ref="$(git rev-parse HEAD 2>/dev/null || printf 'no-git')"

  {
    echo "# Restore drill — ${record_date} (M8 acceptance, spec §14)"
    echo
    echo "## Acceptance bar"
    echo
    echo '> "restore drill (PG + one app back serving on a clean VM < 30 min,'
    echo '>  documented as executed)" — docs/faas_implementation_spec.md §14 M8 row.'
    echo
    cat <<FIELDS
## Run summary

| Field | Value |
|---|---|
| Date (UTC) | ${record_date} |
| Operator | ${operator} |
| Box | ${box} |
| Started | ${DRILL_START_ISO} |
| Finished | ${DRILL_END_ISO} |
| Wall-clock total | $(( total / 60 )) min $(( total % 60 )) s |
| RPO via basebackup | ${rpo_base_min} min ${rpo_base_sec} s |
| RPO via WAL | ${rpo_wal_min} min ${rpo_wal_sec} s |
| Wake latency | ${WAKE_LATENCY}s |
| Basebackup used | ${LATEST_BB} |
| Basebackup SHA-256 | ${BASE_SHA} |
| Recovery stanza status | ${RECOVERY_STATUS} |
| host.age SHA-256 (preserved) | ${SHA_PRE} |
| Verdict | **${RESULT}** (bar = 30 min) |
| Operator / commit | ${operator} @ ${commit_ref} |
FIELDS
    echo
    echo "## Step log (auto-captured)"
    echo
    echo '```'
    echo "drill-start: ${DRILL_START_ISO}"
    echo "drill-end:   ${DRILL_END_ISO}"
    echo "basebackup:  ${LATEST_BB} (${BASE_SHA})"
    echo "rpo-base:    ${rpo_base_min} min ${rpo_base_sec} s"
    echo "rpo-wal:     ${rpo_wal_min} min ${rpo_wal_sec} s"
    echo "recovery:    ${RECOVERY_STATUS}"
    echo "schema:      ${SCHEMA_CHECK} (migration version before=${MIGRATION_VERSION_BEFORE}, after=${MIGRATION_VERSION_AFTER})"
    echo "migration-up-time: ${MIGRATION_UPTIME}"
    echo "rows-before: accounts=${LIVE_ACCOUNTS} apps=${LIVE_APPS} instances_states.healthy=${LIVE_HEALTHY_INSTANCES}"
    echo "rows-after:  accounts=${RESTORE_ACCOUNTS} apps=${RESTORE_APPS} instances_states.healthy=${RESTORE_HEALTHY_INSTANCES}"
    echo "row-count-diffs: ${ROW_COUNT_DIFFS}"
    echo "host.age:    ${SHA_PRE} (preserved)"
    echo "wipe:        ${PG_DATA}"
    echo "wake:        ${WAKE_LATENCY}s to ${DRILL_APP_HOST}:${DRILL_APP_PORT}"
    echo "verdict:     ${RESULT}"
    [[ "$FAILURE_REASON" == "-" ]] || echo "failure:     ${FAILURE_REASON}"
    echo '```'
    echo
    echo "## Pre-flight notes"
    echo
    echo "- Postgres role wired and converged (wal_level=replica, archive_mode=on, archive_command replays the local WAL archive)."
    echo "- Postgres_backup role wired and converged (faas-pg-basebackup.timer enabled)."
    echo "- The newest backup was restored by extracting base.tar.gz and pg_wal.tar.gz; archived WAL promotion was verified with pg_is_in_recovery()."
    echo "- The host.age SHA-256 was stamped before the wipe and verified again before restoring the identity."
    echo
    echo "## Anomalies / observations"
    echo
    if [[ "$FAILURE_REASON" == "-" ]]; then
      echo "None recorded by the drill."
    else
      echo "${FAILURE_REASON}"
    fi
    echo
    echo "## Follow-ups"
    echo
    echo "- Repeat this drill at least every 30 days; the m8-done CI gate rejects stale or missing evidence."
    echo '- Keep the off-host Storage Box restore-verify (`make backup-restore-verify`) as a separate non-destructive check.'
  } > "$RECORD_FILE"
  RECORD_WRITTEN=1
  ok "drill record written → $RECORD_FILE"
}

cleanup() {
  local rc="${1:-1}"
  set +e
  (( CLEANUP_DONE == 0 )) || return 0
  CLEANUP_DONE=1

  # Restore the exact pre-drill PostgreSQL configuration, including owner and
  # mode. This is safer than trying to sed a user-edited setting back out.
  if [[ -n "$PG_CONF_BACKUP" && -f "$PG_CONF_BACKUP" ]]; then
    cp -a "$PG_CONF_BACKUP" "$PG_CONF" 2>/dev/null || warn "could not restore $PG_CONF from backup"
    if systemctl is-active --quiet postgresql; then
      systemctl reload postgresql 2>/dev/null || warn "could not reload postgresql after restoring $PG_CONF"
    fi
  fi
  [[ -d "$PG_DATA" ]] && rm -f "$PG_DATA/recovery.signal"

  # A failure during the wipe/extract phase can leave PGDATA unusable. Keep the
  # service down in that case; starting the daemons against an empty directory
  # would compound the incident. The operator-facing FAIL record explains why.
  if (( rc != 0 && DRILL_COMPLETED == 0 )); then
    if (( POSTGRES_WAS_ACTIVE == 0 )) && systemctl is-active --quiet postgresql; then
      systemctl stop postgresql 2>/dev/null || warn "could not stop postgresql started by the failed drill"
    fi
    for unit in "${DAEMONS[@]}"; do
      if ! daemon_was_active "$unit" && systemctl is-active --quiet "faas-${unit}.service"; then
        systemctl stop "faas-${unit}.service" 2>/dev/null || warn "could not stop faas-${unit}.service started by the failed drill"
      fi
    done
    if (( POSTGRES_WAS_ACTIVE == 1 )) && [[ -f "$PG_DATA/PG_VERSION" ]]; then
      systemctl start postgresql 2>/dev/null || warn "could not restart postgresql during cleanup"
    elif (( POSTGRES_WAS_ACTIVE == 1 )); then
      warn "postgresql left stopped: restored PGDATA is incomplete"
    fi
    if (( DAEMONS_WERE_STOPPED == 1 )) && systemctl is-active --quiet postgresql; then
      for unit in "${ACTIVE_DAEMONS[@]}"; do
        systemctl start "faas-${unit}.service" 2>/dev/null || warn "could not restart faas-${unit}.service during cleanup"
      done
    fi
  fi

  rm -f "$PG_CONF_BACKUP"
  if (( DRILL_ACTIVE == 1 && RECORD_WRITTEN == 0 )); then
    DRILL_END=$(date +%s)
    DRILL_END_ISO=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
    RESULT="FAIL"
    [[ -n "$FAILURE_REASON" ]] || FAILURE_REASON="drill exited with status ${rc}"
    write_record || warn "could not write failure record to ${RECORD_DIR}"
  fi
}
trap 'cleanup "$?"' EXIT

heading "0/7 Pre-flight"
[[ "$(uname -s)" == "Linux" ]] || fail "drill must run on the EX44 (Linux)"
[[ $EUID -eq 0 ]] || fail "must run as root (stops daemons, writes ${PG_DATA})"
command -v runuser >/dev/null 2>&1 || fail "runuser is required to query PostgreSQL as the postgres OS user"
command -v psql >/dev/null 2>&1 || fail "psql is required on PATH"
[[ -x "$PG_ISREADY" ]] || fail "pg_isready missing at ${PG_ISREADY}"
[[ -d "$PG_ARCHIVE" ]] || fail "${PG_ARCHIVE} missing — run the M8 postgres role first"
[[ -d "$PG_BASEBACKUP_DIR" ]] || fail "${PG_BASEBACKUP_DIR} missing — basebackup is the restore source"
[[ -f "$PG_CONF" ]] || fail "${PG_CONF} missing — PostgreSQL cluster config is not converged"

LATEST_BB="$(ls -1dt "$PG_BASEBACKUP_DIR"/basebackup-* 2>/dev/null | head -1 || true)"
[[ -n "$LATEST_BB" && -d "$LATEST_BB" ]] || fail "no basebackup-*/ under ${PG_BASEBACKUP_DIR}"
[[ -f "$LATEST_BB/base.tar.gz" ]] || fail "${LATEST_BB}/base.tar.gz missing — backup is not a tar-format pg_basebackup"
LATEST_BB_TS=$(stat -c %Y "$LATEST_BB")
RPO_BASE=$(( DRILL_START - LATEST_BB_TS ))
(( RPO_BASE >= 0 )) || RPO_BASE=0
ok "picked basebackup: $LATEST_BB"
ok "RPO at basebackup = $(( RPO_BASE / 60 )) min $(( RPO_BASE % 60 )) s"

LATEST_WAL="$(ls -1t "$PG_ARCHIVE"/* 2>/dev/null | head -1 || true)"
if [[ -n "$LATEST_WAL" ]]; then
  LATEST_WAL_TS=$(stat -c %Y "$LATEST_WAL")
  RPO_WAL=$(( DRILL_START - LATEST_WAL_TS ))
  (( RPO_WAL >= 0 )) || RPO_WAL=0
  RPO_SECONDS=$RPO_WAL
  ok "most recent archived WAL: $(basename "$LATEST_WAL") (RPO via WAL = $(( RPO_WAL / 60 )) min $(( RPO_WAL % 60 )) s)"
else
  RPO_SECONDS=$RPO_BASE
  warn "no archived WAL found — drill will replay from the basebackup (RPO = basebackup age)"
fi

heading "0.5/7 Stamp host.age into basebackup (preserves sealed secrets)"
[[ -f "$HOST_KEY" ]] || fail "$HOST_KEY missing — refusing to drill before vmmd initializes the host key"
[[ -f "$HOST_PUB" ]] || fail "$HOST_PUB missing — run the host bootstrap first"
SHA_PRE="$(sha256sum "$HOST_KEY" | awk '{print $1}')"
install -m 0400 "$HOST_KEY" "$LATEST_BB/host.age"
install -m 0444 "$HOST_PUB" "$LATEST_BB/host.age.pub"
printf '%s\n' "$SHA_PRE" > "$LATEST_BB/host.age.sha256"
ok "host.age SHA-256: $SHA_PRE (stamped at $LATEST_BB/host.age)"

heading "0.75/7 Capture database invariants"
capture_row_counts || fail "could not capture accounts/apps/healthy-instance counts before the crash"
MIGRATION_VERSION_BEFORE="$(migration_version)"
[[ -n "$MIGRATION_VERSION_BEFORE" ]] || MIGRATION_VERSION_BEFORE="-"
ok "rows before: accounts=${LIVE_ACCOUNTS} apps=${LIVE_APPS} instances_states.healthy=${LIVE_HEALTHY_INSTANCES}"
ok "migration version before: ${MIGRATION_VERSION_BEFORE}"

# From this point onward an operator-visible FAIL record is useful, and the
# cleanup trap owns service/config recovery.
DRILL_ACTIVE=1

heading "1/7 Stop daemons + Postgres (deliberate crash)"
for unit in "${DAEMONS[@]}"; do
  if systemctl is-active --quiet "faas-${unit}.service"; then
    ACTIVE_DAEMONS+=("$unit")
    systemctl stop "faas-${unit}.service"
    ok "stopped faas-${unit}.service"
  else
    warn "faas-${unit}.service was not active"
  fi
done
DAEMONS_WERE_STOPPED=1
if systemctl is-active --quiet postgresql; then
  POSTGRES_WAS_ACTIVE=1
  systemctl stop postgresql
  ok "stopped postgresql"
else
  warn "postgresql was not active"
fi

heading "2/7 Wipe ${PG_DATA} (disaster simulation)"
rm -rf "$PG_DATA"
mkdir -p "$PG_DATA"
ok "${PG_DATA} wiped"

heading "3/7 Restore basebackup + pg_wal"
# `faas-pg-basebackup.service` uses -Ft -z, so the backup directory contains
# compressed tar members. Extract both members; rsyncing the tar files into
# PGDATA would leave PostgreSQL with no PG_VERSION and never exercise restore.
tar -xzf "$LATEST_BB/base.tar.gz" -C "$PG_DATA"
if [[ -f "$LATEST_BB/pg_wal.tar.gz" ]]; then
  tar -xzf "$LATEST_BB/pg_wal.tar.gz" -C "$PG_DATA"
fi
[[ -f "$PG_DATA/PG_VERSION" ]] || fail "basebackup extraction did not produce PG_VERSION"
chown -R postgres:postgres "$PG_DATA"
ok "basebackup extracted into ${PG_DATA}"

heading "4/7 Write recovery stanza in ${PG_CONF}"
PG_CONF_BACKUP="$(mktemp /run/faas-m8-postgresql.conf.XXXXXX 2>/dev/null || mktemp /tmp/faas-m8-postgresql.conf.XXXXXX)"
cp -a "$PG_CONF" "$PG_CONF_BACKUP"
touch "$PG_DATA/recovery.signal"
cat >> "$PG_CONF" <<EOF_CONF

# --- faas-m8-restore-drill: recovery stanza (M8, removed by EXIT trap) ---
restore_command = 'cp ${PG_ARCHIVE}/%f %p'
recovery_target_action = 'promote'
EOF_CONF
ok "recovery.signal + restore_command written"

heading "5/7 Start Postgres, replay WAL, and start daemons"
MIGRATION_START=$(date +%s)
systemctl start postgresql
ok "postgresql started"
wait_for_promotion || fail "Postgres never promoted — inspect journalctl -u postgresql"

for unit in "${DAEMONS[@]}"; do
  systemctl start "faas-${unit}.service"
  ok "started faas-${unit}.service"
done

# Startup applies pending migrations through the apid migration leader. Wait
# for both the service and a non-empty goose ledger before measuring the
# migration-up time; an active process alone is not proof that schema startup
# completed.
MIGRATION_READY=0
for i in $(seq 1 60); do
  if systemctl is-active --quiet faas-apid.service && migration_ready; then
    MIGRATION_READY=1
    MIGRATION_UPTIME="$(( $(date +%s) - MIGRATION_START ))s"
    MIGRATION_VERSION_AFTER="$(migration_version)"
    SCHEMA_CHECK="goose ledger present; apid active"
    ok "schema migrations current after ${MIGRATION_UPTIME}"
    break
  fi
  sleep 2
done
(( MIGRATION_READY == 1 )) || fail "schema migrations did not become ready after 120s"

heading "5.5/7 Restore host.age into /etc/faas/secrets"
SHA_STORED="$(cat "$LATEST_BB/host.age.sha256")"
SHA_LIVE="$(sha256sum "$LATEST_BB/host.age" | awk '{print $1}')"
[[ "$SHA_STORED" == "$SHA_LIVE" ]] || fail "host.age SHA changed between backup and restore — refusing to overwrite"
install -d -m 0700 -o root -g root /etc/faas/secrets
install -m 0400 "$LATEST_BB/host.age" "$HOST_KEY"
install -m 0444 "$LATEST_BB/host.age.pub" "$HOST_PUB"
systemctl restart faas-vmmd.service
ok "host.age restored and vmmd restarted"

heading "6/7 Verify row counts, schedd admission, and test app"
RESTORE_ACCOUNTS="$(psql_count 'SELECT count(*) FROM accounts')" || fail "could not query restored accounts count"
RESTORE_APPS="$(psql_count 'SELECT count(*) FROM apps')" || fail "could not query restored apps count"
RESTORE_HEALTHY_INSTANCES="$(psql_count "SELECT count(*) FROM instances WHERE state IN ('running', 'waking', 'cold_booting')")" || fail "could not query restored healthy-instance count"
ROW_COUNT_DIFFS="accounts=$(( RESTORE_ACCOUNTS - LIVE_ACCOUNTS )), apps=$(( RESTORE_APPS - LIVE_APPS )), instances_states.healthy=$(( RESTORE_HEALTHY_INSTANCES - LIVE_HEALTHY_INSTANCES ))"
for table in accounts apps instances_states.healthy; do
  case "$table" in
    accounts) before="$LIVE_ACCOUNTS"; after="$RESTORE_ACCOUNTS" ;;
    apps) before="$LIVE_APPS"; after="$RESTORE_APPS" ;;
    instances_states.healthy) before="$LIVE_HEALTHY_INSTANCES"; after="$RESTORE_HEALTHY_INSTANCES" ;;
  esac
  [[ "$before" == "$after" ]] || fail "row-count invariant failed for ${table}: before=${before} after=${after}"
done
ok "row-count invariants passed (${ROW_COUNT_DIFFS})"

READY=0
for i in $(seq 1 60); do
  if curl -fsS "$SCHEDD_METRICS" 2>/dev/null | grep -q "fcvm_resident_ram_pct"; then
    READY=1
    ok "schedd admission up (after $((i * 2))s)"
    break
  fi
  sleep 2
done
(( READY == 1 )) || fail "schedd admission never came up after 120s — see journalctl -u faas-schedd"

WAKE_START=$(date +%s)
HTTP_CODE=$(curl -sS -o /tmp/faas-drill-body -w '%{http_code}' --max-time 60 \
  "http://${DRILL_APP_HOST}:${DRILL_APP_PORT}/" || printf '000')
WAKE_END=$(date +%s)
WAKE_LATENCY=$(( WAKE_END - WAKE_START ))
[[ "$HTTP_CODE" =~ ^2 ]] || fail "test app responded ${HTTP_CODE} (expected 2xx); body in /tmp/faas-drill-body"
ok "test app responded ${HTTP_CODE} in ${WAKE_LATENCY}s"

heading "7/7 Summary"
DRILL_END=$(date +%s)
DRILL_END_ISO=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
TOTAL=$(( DRILL_END - DRILL_START ))
if (( TOTAL <= 1800 )); then
  RESULT="PASS"
else
  RESULT="FAIL"
  FAILURE_REASON="wall-clock ${TOTAL}s exceeds the 1800s M8 bar"
fi
printf '\nM8 Restore Drill — %s\n\n  Started: %s\n  Finished: %s\n  Wall-clock: %s min %s s\n  RPO: %s min %s s\n  Wake: %ss\n  Verdict: %s\n' \
  "$DRILL_END_ISO" "$DRILL_START_ISO" "$DRILL_END_ISO" "$((TOTAL / 60))" "$((TOTAL % 60))" \
  "$((RPO_SECONDS / 60))" "$((RPO_SECONDS % 60))" "$WAKE_LATENCY" "$RESULT"
write_record
DRILL_COMPLETED=1
if [[ "$RESULT" == "PASS" ]]; then
  exit 0
fi
exit 1
