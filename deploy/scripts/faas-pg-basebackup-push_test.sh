#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PUSHER="$SCRIPT_DIR/faas-pg-basebackup-push.sh"
ROOT="$(mktemp -d)"
trap 'rm -rf "$ROOT"' EXIT
SRC="$ROOT/basebackup"
STATE="$ROOT/state"
BIN="$ROOT/bin"
CONF="$ROOT/rclone.conf"
mkdir -p "$SRC" "$BIN"
printf '[offhostbox]\ntype = memory\n' >"$CONF"

cat >"$BIN/rclone" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
echo "$*" >>"$MOCK_RCLONE_LOG"
if [[ "$1" == check && "${MOCK_RCLONE_FAIL:-0}" == 1 ]]; then
  exit 9
fi
EOF
cat >"$BIN/install" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
dest="${@: -1}"
mkdir -p "$dest"
EOF
chmod +x "$BIN/rclone" "$BIN/install" "$PUSHER"

for stamp in 2026-01-01T000000Z 2026-01-02T000000Z 2026-01-03T000000Z; do
  dir="$SRC/basebackup-$stamp"
  mkdir -p "$dir"
  printf archive >"$dir/base.tar.gz"
  printf manifest >"$dir/backup_manifest"
done

run_pusher() {
  PATH="$BIN:$PATH" \
    FAAS_PG_BASEBACKUP_ROOT="$SRC" \
    FAAS_PG_BACKUP_STATE_ROOT="$STATE" \
    FAAS_OFF_HOST_BACKUP_RCLONE_CONFIG="$CONF" \
    FAAS_PG_LOCAL_BASEBACKUP_KEEP=2 \
    MOCK_RCLONE_FAIL="${MOCK_RCLONE_FAIL:-0}" \
    MOCK_RCLONE_LOG="$ROOT/rclone.log" \
    "$PUSHER"
}

if MOCK_RCLONE_FAIL=1 run_pusher >/dev/null 2>&1; then
  echo 'expected remote verification failure' >&2
  exit 1
fi
[[ "$(find "$SRC" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ')" == 3 ]]

run_pusher >/dev/null
[[ ! -e "$SRC/basebackup-2026-01-01T000000Z" ]]
[[ -d "$SRC/basebackup-2026-01-02T000000Z" ]]
[[ -d "$SRC/basebackup-2026-01-03T000000Z" ]]
[[ -s "$STATE/basebackup-push-success" ]]
echo "PASS: basebackup push verifies the remote before pruning local recovery points"

# A host's private age identity stamped into a backup (as the M8 restore drill
# once did) is scrubbed locally and never part of the copy or its check.
stamped="$SRC/basebackup-2026-01-03T000000Z"
printf 'AGE-SECRET-KEY-1TEST\n' >"$stamped/host.age"
printf 'age1test\n' >"$stamped/host.age.pub"
printf 'cafef00d\n' >"$stamped/host.age.sha256"
: >"$ROOT/rclone.log"
run_pusher >/dev/null
for name in host.age host.age.pub host.age.sha256; do
  [[ ! -e "$stamped/$name" ]] || { echo "FAIL: $name left in the pushed tree" >&2; exit 1; }
done
for verb in copy check; do
  line="$(grep "^$verb " "$ROOT/rclone.log")"
  [[ "$line" == *"--exclude host.age "* && "$line" == *"--exclude host.age.pub "* && "$line" == *"--exclude host.age.sha256 "* ]] \
    || { echo "FAIL: rclone $verb does not exclude the host identity: $line" >&2; exit 1; }
done
[[ -s "$stamped/base.tar.gz" ]]
echo "PASS: basebackup push never sends a host age identity off-host"
