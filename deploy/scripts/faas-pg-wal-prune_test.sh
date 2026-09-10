#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PRUNER="$SCRIPT_DIR/faas-pg-wal-prune.sh"
ROOT="$(mktemp -d)"
trap 'rm -rf "$ROOT"' EXIT

ARCHIVE="$ROOT/archive"
BACKUPS="$ROOT/basebackup"
STATE="$ROOT/state"
BIN="$ROOT/bin"
CONF="$ROOT/rclone.conf"
mkdir -p "$ARCHIVE" "$BACKUPS" "$BIN"
printf '[offhostbox]\ntype = memory\n' >"$CONF"

cat >"$BIN/pg_archivecleanup" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[[ "$1" == -n ]]
root="$2"
boundary="$3"
for path in "$root"/*; do
  [[ -f "$path" ]] || continue
  name="$(basename "$path")"
  if [[ "$name" =~ ^[0-9A-F]{24}$ && "$name" < "$boundary" ]]; then
    printf '%s\n' "$path"
  fi
done
EOF

cat >"$BIN/rclone" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$1" == check && "${MOCK_RCLONE_FAIL:-0}" == 1 ]]; then
  exit 9
fi
[[ "$1" == copy || "$1" == check ]]
for arg in "$@"; do
  case "$arg" in
    --files-from=*) list="${arg#--files-from=}" ;;
  esac
done
[[ -s "${list:-}" ]]
EOF
cat >"$BIN/stat" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[[ "$1" == -c && "$2" == '%s %Y' ]]
printf '%s %s\n' "$(wc -c <"$3" | tr -d ' ')" 1704067200
EOF
chmod +x "$BIN/pg_archivecleanup" "$BIN/rclone" "$BIN/stat" "$PRUNER"

make_backup() {
  local name="$1" boundary="$2" with_manifest="${3:-true}"
  local dir="$BACKUPS/$name" payload="$ROOT/payload"
  rm -rf "$payload"
  mkdir -p "$dir" "$payload"
  printf 'START WAL LOCATION: 0/0 (file %s)\n' "$boundary" >"$payload/backup_label"
  tar -czf "$dir/base.tar.gz" -C "$payload" backup_label
  if [[ "$with_manifest" == true ]]; then
    printf '{}\n' >"$dir/backup_manifest"
  fi
}

run_pruner() {
  PATH="$BIN:$PATH" \
    FAAS_PG_WAL_ARCHIVE_ROOT="$ARCHIVE" \
    FAAS_PG_BASEBACKUP_ROOT="$BACKUPS" \
    FAAS_PG_BACKUP_STATE_ROOT="$STATE" \
    FAAS_OFF_HOST_BACKUP_RCLONE_CONFIG="$CONF" \
    OFF_HOST_BACKUP_REMOTE=offhostbox \
    OFF_HOST_BACKUP_WAL_PATH=bucket/wal \
    MOCK_RCLONE_FAIL="${MOCK_RCLONE_FAIL:-0}" \
    "$PRUNER" "$@"
}

old=000000010000000000000010
boundary=000000010000000000000020
new=000000010000000000000030
printf old >"$ARCHIVE/$old"
printf boundary >"$ARCHIVE/$boundary"
printf new >"$ARCHIVE/$new"
make_backup basebackup-2026-01-01T000000Z "$boundary"

out="$(run_pruner)"
grep -Fq 'dry-run verified 1 files' <<<"$out"
[[ -f "$ARCHIVE/$old" ]]

if MOCK_RCLONE_FAIL=1 run_pruner --apply >/dev/null 2>&1; then
  echo 'expected off-host verification failure' >&2
  exit 1
fi
[[ -f "$ARCHIVE/$old" ]]
[[ ! -e "$STATE/wal-prune-success" ]]

run_pruner --apply >/dev/null
[[ ! -e "$ARCHIVE/$old" ]]
[[ -e "$ARCHIVE/$boundary" ]]
[[ -e "$ARCHIVE/$new" ]]
[[ -s "$STATE/wal-prune-success" ]]
grep -Fq 'bytes=' "$STATE/wal-archive-stats"

rm -rf "$BACKUPS" "$STATE"
mkdir -p "$BACKUPS"
make_backup basebackup-2026-01-01T000000Z "$boundary" false
if run_pruner --apply >/dev/null 2>&1; then
  echo 'expected missing-manifest failure' >&2
  exit 1
fi
[[ -e "$ARCHIVE/$boundary" ]]
[[ -e "$ARCHIVE/$new" ]]

echo 'PASS: WAL prune requires off-host verification and respects the oldest retained backup boundary'
