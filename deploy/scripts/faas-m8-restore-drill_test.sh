#!/usr/bin/env bash
# Smoke unit for the M8 restore drill script. Asserts syntax + token presence
# in the operator-facing template. Catches three regression classes:
#
#   1. Syntax breakage in deploy/scripts/faas-m8-restore-drill.sh — `bash -n`
#      without executing the script. Catches missing closes, typo'd `[[`/`]]`,
#      and unterminated heredocs.
#   2. Drift in the record field labels of the template + script body. The
#      bash heredoc in the script's step 7 emits each label literally; a
#      refactor that drops one silently breaks the §14 M8 audit trail. The
#      same labels are locked by pkg/drills/record_test.go via the embedded
#      template, plus TestRecord_BashScriptAndGoRendererAgree which diffs
#      the bash heredoc against the Go renderer's RequiredTokens slice.
#   3. Split-role recovery contracts — only previously active services restart,
#      host.age is optional on a control-plane-only host, writes are quiesced
#      before the invariant/WAL boundary, and preflight cannot mutate the host.
#
# The 15 labels below MUST match pkg/drills/record.go's RequiredTokens slice
# AND the row labels in deploy/scripts/faas-m8-restore-drill.sh's `cat <<FIELDS`
# heredoc. Drift between any of the three is caught by TestRecord_BashScriptAndGoRendererAgree.
#
# Runs as part of `make lint-drill`. Exit 0 on success.

set -euo pipefail

SCRIPT="$(cd "$(dirname "$0")" && pwd)/faas-m8-restore-drill.sh"
EVIDENCE_CHECK="$(cd "$(dirname "$0")" && pwd)/check-restore-drill-evidence.sh"
TEMPLATE="$(cd "$(dirname "$0")" && pwd)/../../docs/drills/TEMPLATE-restore-drill.md"
REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BASEBACKUP_SERVICE="$REPO_ROOT/deploy/systemd/faas-pg-basebackup.service"
BASEBACKUP_TIMER="$REPO_ROOT/deploy/systemd/faas-pg-basebackup.timer"
BASEBACKUP_PUSH_TIMER="$REPO_ROOT/deploy/systemd/faas-pg-basebackup-push.timer"
POSTGRES_ROLE="$REPO_ROOT/deploy/ansible/roles/postgres/tasks/main.yml"
PEER_ACCESS_ROLE="$REPO_ROOT/deploy/ansible/roles/control_plane_peer_access/tasks/main.yml"

# 1. Syntax check on the drill script. Does NOT execute.
bash -n "$SCRIPT" || { echo "FAIL: bash -n $SCRIPT"; exit 1; }
echo "OK: bash -n"
bash -n "$EVIDENCE_CHECK" || { echo "FAIL: bash -n $EVIDENCE_CHECK"; exit 1; }
echo "OK: bash -n evidence freshness check"

# 2. Required record labels present in the template the Go test embeds.
#    Mirrors pkg/drills/record.go:RequiredTokens.
for tok in "Date (UTC)" "Operator" "Box" "Started" "Finished" \
           "Wall-clock total" "RPO via basebackup" "RPO via WAL" \
           "Wake latency" "Basebackup used" "Basebackup SHA-256" \
           "Recovery stanza status" "host.age SHA-256 (preserved)" \
           "Verdict" "Operator / commit"; do
  grep -q "$tok" "$TEMPLATE" || { echo "FAIL: missing token '$tok' in $TEMPLATE"; exit 1; }
done
echo "OK: required tokens present in template"

# 3. Required record labels present in the script body (step 7 heredoc).
#    Catches drift in the bash heredoc that the Go test cannot see alone.
for tok in "Date (UTC)" "Operator" "Box" "Started" "Finished" \
           "Wall-clock total" "RPO via basebackup" "RPO via WAL" \
           "Wake latency" "Basebackup used" "Basebackup SHA-256" \
           "Recovery stanza status" "host.age SHA-256 (preserved)" \
           "Verdict" "Operator / commit"; do
  grep -q "$tok" "$SCRIPT" || { echo "FAIL: missing token '$tok' in $SCRIPT"; exit 1; }
done
echo "OK: required tokens present in drill script"

# 4. Host identity is preserved where present and may be absent only as a
#    complete key pair on split control-plane hosts.
grep -q "0.75/7 Stamp host.age into basebackup when this role owns it" "$SCRIPT" \
  || { echo "FAIL: missing role-aware host.age stamp step"; exit 1; }
grep -q "5.5/7 Restore host.age into /etc/faas/secrets when this role owns it" "$SCRIPT" \
  || { echo "FAIL: missing step 5.5 header"; exit 1; }
grep -q "host.age.sha256" "$SCRIPT" \
  || { echo "FAIL: missing host.age SHA sidecar logic"; exit 1; }
grep -q 'both exist or both be absent' "$SCRIPT" \
  || { echo "FAIL: incomplete host identity pair is not rejected"; exit 1; }
grep -q 'daemon_was_active vmmd' "$SCRIPT" \
  || { echo "FAIL: vmmd restart is not limited to its original role"; exit 1; }
echo "OK: role-aware host.age preservation steps present"

# 5. The nightly producer uses tar format with `-X fetch`, which embeds required
#    WAL in base.tar.gz. The optional pg_wal.tar.gz branch keeps compatibility
#    with separately streamed backups; copying either archive is not a restore.
grep -q 'tar -xzf "\$LATEST_BB/base.tar.gz"' "$SCRIPT" \
  || { echo "FAIL: drill does not extract base.tar.gz"; exit 1; }
grep -q 'pg_wal.tar.gz' "$SCRIPT" \
  || { echo "FAIL: drill does not restore pg_wal.tar.gz"; exit 1; }

# 6. A failed drill must leave an audit record and clean up its recovery
#    stanza. These markers protect the destructive operator path from future
#    early-exit regressions.
grep -q 'trap.*cleanup' "$SCRIPT" \
  || { echo "FAIL: missing EXIT cleanup trap"; exit 1; }
grep -q 'row-count invariant' "$SCRIPT" \
  || { echo "FAIL: missing exact row-count invariant check"; exit 1; }
grep -q 'migration-up-time' "$SCRIPT" \
  || { echo "FAIL: missing migration-up-time evidence"; exit 1; }
grep -q 'pg_is_in_recovery' "$SCRIPT" \
  || { echo "FAIL: missing explicit promotion check"; exit 1; }
echo "OK: tar restore, cleanup, migration, row-count, and promotion checks present"

# 7. Production split-role safety. The successful path must restart only the
#    captured ACTIVE_DAEMONS set. Quiescing the daemons must happen before row
#    capture and the forced WAL boundary. --preflight-only exits before either.
grep -q 'for unit in "${ACTIVE_DAEMONS\[@\]}"' "$SCRIPT" \
  || { echo "FAIL: active-daemon restart contract missing"; exit 1; }
if grep -q 'for unit in "${DAEMONS\[@\]}"; do[[:space:]]*$' "$SCRIPT"; then
  # Inventory loops are allowed, but the start command may not appear in their
  # body. A direct fixed-list start would activate compute services on the CP.
  ! awk '/for unit in "\$\{DAEMONS\[@\]\}"; do/{fixed=1} fixed && /systemctl start "faas-\$\{unit\}\.service"/{bad=1} fixed && /^[[:space:]]*done$/{fixed=0} END{exit bad ? 0 : 1}' "$SCRIPT" \
    || { echo "FAIL: success path starts the full fixed daemon inventory"; exit 1; }
fi
stop_line="$(grep -n 'systemctl stop "faas-${unit}.service"' "$SCRIPT" | tail -1 | cut -d: -f1)"
count_line="$(grep -n 'capture_row_counts || fail' "$SCRIPT" | tail -1 | cut -d: -f1)"
wal_line="$(grep -n "SELECT pg_walfile_name(pg_switch_wal())" "$SCRIPT" | cut -d: -f1)"
[[ -n "$stop_line" && -n "$count_line" && -n "$wal_line" && "$stop_line" -lt "$count_line" && "$count_line" -lt "$wal_line" ]] \
  || { echo "FAIL: writes are not quiesced before row capture and WAL switch"; exit 1; }
grep -q -- '--preflight-only' "$SCRIPT" \
  || { echo "FAIL: non-mutating preflight mode missing"; exit 1; }
grep -q 'DRILL_ACTIVE == 1' "$SCRIPT" \
  || { echo "FAIL: failure cleanup is not guarded from preflight-only execution"; exit 1; }
grep -q "SHOW data_directory" "$SCRIPT" \
  || { echo "FAIL: destructive target is not derived from live PostgreSQL"; exit 1; }
grep -q 'FAAS_PG_DATA=.*does not match PostgreSQL data_directory' "$SCRIPT" \
  || { echo "FAIL: explicit PGDATA mismatch is not rejected"; exit 1; }
grep -q 'FAAS_DRILL_APP_URL' "$SCRIPT" \
  || { echo "FAIL: configurable public recovery app URL missing"; exit 1; }
grep -q 'outside the <350 ms platform snapshot-restore SLO' "$SCRIPT" \
  || { echo "FAIL: recovery probe is not distinguished from platform restore latency"; exit 1; }
echo "OK: split-role, quiesced-WAL, preflight, and latency-scope contracts present"

# 8. Validate the production basebackup producer contract. systemd expands a
#    single `%` in ExecStart as a unit specifier, so date's format characters
#    must be doubled. The calendar form below is accepted by Ubuntu 24.04's
#    systemd; the formerly used ISO-like `T...Z` form is rejected.
grep -Fq 'date -u +%%Y-%%m-%%dT%%H%%M%%SZ' "$BASEBACKUP_SERVICE" \
  || { echo "FAIL: basebackup service date format is not systemd-escaped"; exit 1; }
grep -Fq 'rm -rf -- "$${out}"' "$BASEBACKUP_SERVICE" \
  || { echo "FAIL: basebackup service does not clean a failed partial backup"; exit 1; }
grep -Fq 'OnCalendar=*-*-* 03:00:00 UTC' "$BASEBACKUP_TIMER" \
  || { echo "FAIL: local basebackup timer has an incompatible calendar"; exit 1; }
grep -Fq 'OnCalendar=*-*-* 03:30:00 UTC' "$BASEBACKUP_PUSH_TIMER" \
  || { echo "FAIL: basebackup push timer has an incompatible calendar"; exit 1; }
if command -v systemd-analyze >/dev/null 2>&1; then
  systemd-analyze calendar '*-*-* 03:00:00 UTC' >/dev/null
  systemd-analyze calendar '*-*-* 03:30:00 UTC' >/dev/null
fi

# 9. pg_basebackup connects to the replication pseudo-database. PostgreSQL's
#    `local all postgres peer` HBA rule does not match replication traffic;
#    both topology renderers must keep the narrow local peer rule.
for role in "$POSTGRES_ROLE" "$PEER_ACCESS_ROLE"; do
  grep -Eq '^ +local +replication +postgres +peer$' "$role" \
    || { echo "FAIL: missing local postgres replication peer rule in $role"; exit 1; }
done
echo "OK: basebackup systemd and PostgreSQL replication contracts present"
