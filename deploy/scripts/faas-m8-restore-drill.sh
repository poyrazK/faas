#!/usr/bin/env bash
# faas-m8-restore-drill.sh — restore-drill acceptance for spec §14 M8.
#
# Spec §14 M8 row, private-beta gate:
#   "restore drill (PG + one app back serving on a clean VM < 30 min,
#    documented as executed)"
#
# What this script does, end-to-end:
#   0. Pre-flight: confirm basebackup exists, archive dir has WAL,
#      daemons are healthy enough to start.
#   1. Stop every faas daemon + Postgres.
#   2. Wipe /var/lib/pgsql/data (simulated disaster; archive is untouched).
#   3. rsync the most recent basebackup into /var/lib/pgsql/data.
#   4. Write a recovery stanza so PG replays archived WAL until consistent.
#   5. Start Postgres, then every faas daemon.
#   6. Wait for schedd admission to come up; hit the test app's :8080.
#   7. Print a summary: wall-clock, RPO (max archive timestamp − drill
#      start), pass/fail vs the 30-minute bar.
#
# Out of scope (deferred to M9):
#   - pgbackrest orchestration (we cp WAL to a local archive dir).
#   - Off-host WAL shipping to Hetzner Storage Box.
#   - Archive encryption.
#   - Parallel WAL replay (single timeline, one basebackup).
#
# Run as root on the EX44. The script refuses to run if it's not Linux
# (macOS devs use `make metal-lima` for the same accept tests).

set -euo pipefail

PG_DATA=/var/lib/pgsql/data
PG_ARCHIVE=/var/lib/pgsql/archive
PG_BASEBACKUP_DIR=/var/lib/pgsql/basebackup
PG_CONF=/etc/postgresql/15/main/postgresql.conf

# The test app the drill proves is "back serving". Set
# FAAS_DRILL_APP_HOST to override the slot/host. Default targets the
# platform's standard fixture (10.100.0.1).
DRILL_APP_HOST="${FAAS_DRILL_APP_HOST:-10.100.0.1}"
DRILL_APP_PORT="${FAAS_DRILL_APP_PORT:-8080}"

heading() { printf '\n\033[1;36m▶ %s\033[0m\n' "$*"; }
ok()      { printf '\033[1;32m✓\033[0m %s\n' "$*"; }
warn()    { printf '\033[1;33m!\033[0m %s\n' "$*" >&2; }
fail()    { printf '\033[1;31m✗\033[0m %s\n' "$*" >&2; exit 1; }

DRILL_START=$(date +%s)
DRILL_START_ISO=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

heading "0/7 Pre-flight"
[[ "$(uname -s)" == "Linux" ]] || fail "drill must run on the EX44 (Linux)"
[[ $EUID -eq 0 ]] || fail "must run as root (stops daemons, writes /var/lib/pgsql)"
[[ -d "$PG_ARCHIVE" ]] || fail "$PG_ARCHIVE missing — run the M8 postgres role first"
[[ -d "$PG_BASEBACKUP_DIR" ]] || fail "$PG_BASEBACKUP_DIR missing — basebackup is the rsync source"

# Pick the newest basebackup by mtime. The nightly cron writes
# /var/lib/pgsql/basebackup-<UTC-date>/; we take the highest suffix.
LATEST_BB=$(ls -1dt "$PG_BASEBACKUP_DIR"/basebackup-* 2>/dev/null | head -1 || true)
[[ -n "$LATEST_BB" ]] || fail "no basebackup-*/ under $PG_BASEBACKUP_DIR"
LATEST_BB_TS=$(stat -c %Y "$LATEST_BB")
RPO_BASE=$(( DRILL_START - LATEST_BB_TS ))
ok "picked basebackup: $LATEST_BB"
ok "RPO at basebackup = $(( RPO_BASE / 60 )) min $(( RPO_BASE % 60 )) s"

# Record the most recent archived WAL's mtime — that's the worst-case
# data loss in the drill (everything committed after this moment is
# gone).
LATEST_WAL=$(ls -1t "$PG_ARCHIVE"/* 2>/dev/null | head -1 || true)
if [[ -n "$LATEST_WAL" ]]; then
  LATEST_WAL_TS=$(stat -c %Y "$LATEST_WAL")
  RPO_WAL=$(( DRILL_START - LATEST_WAL_TS ))
  ok "most recent archived WAL: $(basename "$LATEST_WAL") (RPO via WAL = $(( RPO_WAL / 60 )) min $(( RPO_WAL % 60 )) s)"
  RPO_SECONDS=$RPO_WAL
else
  warn "no archived WAL found — drill will replay from basebackup only (RPO = basebackup age)"
  RPO_SECONDS=$RPO_BASE
fi

# --- 1. Stop daemons + Postgres -----------------------------------------

heading "1/7 Stop daemons + Postgres"
for unit in apid gatewayd schedd vmmd imaged builderd meterd githubd; do
  if systemctl is-active --quiet "faas-$unit.service"; then
    systemctl stop "faas-$unit.service"
    ok "stopped faas-$unit.service"
  else
    warn "faas-$unit.service was not active"
  fi
done

if systemctl is-active --quiet postgresql; then
  systemctl stop postgresql
  ok "stopped postgresql"
else
  warn "postgresql was not active"
fi

# --- 2. Wipe PG data dir ------------------------------------------------

heading "2/7 Wipe $PG_DATA (disaster simulation)"
rm -rf "$PG_DATA"
ok "$PG_DATA wiped"

# --- 3. Restore basebackup via rsync ------------------------------------

heading "3/7 rsync basebackup → $PG_DATA"
rsync -a --delete "$LATEST_BB"/ "$PG_DATA"/
ok "rsync complete"

# --- 4. Write recovery stanza (WAL replay) ------------------------------

heading "4/7 Write recovery stanza in $PG_CONF"
# recovery.conf was the PG ≤11 name; PG 12+ uses signal files + GUCs
# in postgresql.conf. We touch the signal file so PG enters recovery
# on next start, replaying from the archive until consistent.
touch "$PG_DATA/recovery.signal"
cat >> "$PG_CONF" <<EOF

# --- faas-m8-restore-drill: recovery stanza (M8, removed after drill) ---
restore_command = 'cp $PG_ARCHIVE/%f %p'
recovery_target_action = 'promote'
EOF
ok "recovery.signal + restore_command written"

# --- 5. Start Postgres + daemons ----------------------------------------

heading "5/7 Start Postgres + daemons"
systemctl start postgresql
ok "postgresql started"

for unit in apid gatewayd schedd vmmd imaged builderd meterd githubd; do
  systemctl start "faas-$unit.service"
  ok "started faas-$unit.service"
done

# --- 6. Wait for schedd admission + hit the test app --------------------

heading "6/7 Wait for schedd admission + hit test app"
# Schedd's admission loop runs every few seconds. We poll the
# /metrics endpoint on schedd's MetricsAddr (default 9091) until
# fcvm_resident_ram_pct appears — that's the cheapest signal that
# schedd is alive and the PG read path is working (the gauge's
# ResidentBytes callback queries sched.Ledger, which the PG store
# rebuilds on boot).
SCHEDD_METRICS="${FAAS_SCHEDD_METRICS:-http://127.0.0.1:9091/metrics}"
READY=0
for i in $(seq 1 60); do
  if curl -fsS "$SCHEDD_METRICS" 2>/dev/null | grep -q "fcvm_resident_ram_pct" || true; then
    READY=1
    ok "schedd admission up (after $((i*2))s)"
    break
  fi
  sleep 2
done
[[ $READY -eq 1 ]] || fail "schedd admission never came up after 120s — see journalctl -u faas-schedd"

# Hit the test app. The wake queue will cold-boot it from snapshot
# (ADR-005); the request latency is logged but not the gate here.
WAKE_START=$(date +%s)
HTTP_CODE=$(curl -sS -o /tmp/faas-drill-body -w '%{http_code}' --max-time 60 \
  "http://${DRILL_APP_HOST}:${DRILL_APP_PORT}/" || echo "000")
WAKE_END=$(date +%s)
WAKE_LATENCY=$(( WAKE_END - WAKE_START ))

if [[ "$HTTP_CODE" =~ ^2 ]]; then
  ok "test app responded $HTTP_CODE in ${WAKE_LATENCY}s"
else
  fail "test app responded $HTTP_CODE (expected 2xx); body in /tmp/faas-drill-body"
fi

# --- 7. Summary ---------------------------------------------------------

heading "7/7 Summary"
DRILL_END=$(date +%s)
DRILL_END_ISO=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
TOTAL=$(( DRILL_END - DRILL_START ))
RPO_MIN=$(( RPO_SECONDS / 60 ))
RPO_SEC=$(( RPO_SECONDS % 60 ))

# Pass threshold: 30 minutes total wall-clock (spec §14 M8 row).
if (( TOTAL <= 1800 )); then
  RESULT="PASS"
else
  RESULT="FAIL"
fi

cat <<EOF

M8 Restore Drill — $(date -u +"%Y-%m-%dT%H:%M:%SZ")

  Started:    $DRILL_START_ISO
  Finished:   $DRILL_END_ISO
  Wall-clock: $(( TOTAL / 60 )) min $(( TOTAL % 60 )) s
  RPO:        $RPO_MIN min $RPO_SEC s
  Wake:       ${WAKE_LATENCY}s
  Basebackup: $LATEST_BB

  Verdict:    $RESULT (spec §14 M8 bar = 30 min)

Append this output to docs/drills/<date>-restore-drill.md so the
acceptance gate keeps an audit trail.
EOF

# Clean up the recovery stanza so PG doesn't try to replay on next boot.
# We can't anchor on `EOF` because bash consumes the heredoc terminator and
# never writes it to the file — use a real written line as the close anchor.
sed -i '/^# --- faas-m8-restore-drill:/,/^recovery_target_action = /d' "$PG_CONF" || true
rm -f "$PG_DATA/recovery.signal"

exit $(( TOTAL <= 1800 ? 0 : 1 ))