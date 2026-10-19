#!/usr/bin/env bash
# Exercise faas-cp-state-backup.sh with a fake gregalectl and rclone
# (ADR-1344 §8). Run by `make lint-drill`.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BACKUP="$SCRIPT_DIR/faas-cp-state-backup.sh"
ROOT="$(mktemp -d)"
trap 'rm -rf "$ROOT"' EXIT
BIN="$ROOT/bin"
mkdir -p "$BIN"
printf '[offhostbox]\ntype = memory\n' >"$ROOT/rclone.conf"
: >"$ROOT/paths"

cat >"$BIN/gregalectl" <<'EOF'
#!/usr/bin/env bash
# Records the call and writes a fake bundle to --out.
echo "$*" >>"$MOCK_LOG"
while [[ $# -gt 0 ]]; do
  if [[ "$1" == --out ]]; then printf 'age-encryption.org/v1 fake' >"$2"; fi
  shift
done
EOF
cat >"$BIN/rclone" <<'EOF'
#!/usr/bin/env bash
echo "rclone $*" >>"$MOCK_LOG"
[[ "$1" == check && "${MOCK_RCLONE_FAIL:-0}" == 1 ]] && exit 9
exit 0
EOF
chmod +x "$BIN/gregalectl" "$BIN/rclone"

run() {
  FAAS_CP_STATE_BACKUP_RECIPIENTS="$ROOT/recipients" \
    FAAS_CP_STATE_BACKUP_PATHS="$ROOT/paths" \
    FAAS_CP_STATE_BACKUP_DIR="$ROOT/out" \
    FAAS_OFF_HOST_BACKUP_RCLONE_CONFIG="$ROOT/rclone.conf" \
    FAAS_CP_STATE_BACKUP_KEEP=2 \
    FAAS_RCLONE_BIN="$BIN/rclone" \
    FAAS_GREGALECTL="$BIN/gregalectl" \
    FAAS_CP_STATE_BACKUP_TEXTFILE="$ROOT/cp_state.prom" \
    FAAS_CP_STATE_BACKUP_HOST=cp-1 \
    MOCK_LOG="$ROOT/log" \
    MOCK_RCLONE_FAIL="${MOCK_RCLONE_FAIL:-0}" \
    bash "$BACKUP"
}

# Without recipients the backup is off: nothing sealed, nothing pushed.
: >"$ROOT/log"
run 2>/dev/null
[[ ! -s "$ROOT/log" && ! -e "$ROOT/cp_state.prom" ]] || { echo "FAIL: ran without recipients" >&2; exit 1; }

echo "age1examplerecipient" >"$ROOT/recipients"

# A failed remote verification keeps the bundle and writes no success metric.
if MOCK_RCLONE_FAIL=1 run >/dev/null 2>&1; then
  echo "FAIL: a failed remote check passed" >&2; exit 1
fi
[[ ! -e "$ROOT/cp_state.prom" ]] || { echo "FAIL: success recorded for an unverified push" >&2; exit 1; }

# Three good runs keep two local bundles and record the success.
for _ in 1 2 3; do
  run >/dev/null
  sleep 1
done
count="$(find "$ROOT/out" -name 'cp-state-*.tar.age' | wc -l | tr -d ' ')"
[[ "$count" == 2 ]] || { echo "FAIL: kept $count local bundles, want 2" >&2; exit 1; }
grep -q '^faas_cp_state_backup_last_success_timestamp_seconds [0-9]' "$ROOT/cp_state.prom" \
  || { echo "FAIL: no success metric" >&2; exit 1; }
grep -q -- "--recipients $ROOT/recipients" "$ROOT/log" || { echo "FAIL: seal did not use the recipients" >&2; exit 1; }
grep -q "rclone copy .* offhostbox:faas-cp-state/cp-1/" "$ROOT/log" || { echo "FAIL: pushed to the wrong remote path" >&2; exit 1; }
echo "PASS: control-plane state backup seals to recipients, verifies off-host and prunes locally"
