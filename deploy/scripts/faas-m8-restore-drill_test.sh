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
#   3. Step 0.5 + 5.5 host.age preservation markers — without these a clean
#      restore silently bricks every customer sealed secret.
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

# 4. Step 0.5 + step 5.5 host.age preservation markers present.
grep -q "0.5/7 Stamp host.age into basebackup" "$SCRIPT" \
  || { echo "FAIL: missing step 0.5 header"; exit 1; }
grep -q "5.5/7 Restore host.age into /etc/faas/secrets" "$SCRIPT" \
  || { echo "FAIL: missing step 5.5 header"; exit 1; }
grep -q "host.age.sha256" "$SCRIPT" \
  || { echo "FAIL: missing host.age SHA sidecar logic"; exit 1; }
echo "OK: host.age preservation steps present"

# 5. The nightly producer uses tar format. The drill must extract the base
#    and WAL members; rsyncing base.tar.gz into PGDATA is not a restore.
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
