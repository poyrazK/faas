#!/usr/bin/env bash
# Prune local PostgreSQL WAL only after proving every candidate exists
# byte-for-byte in the off-host store. The oldest retained basebackup's
# START WAL file is the lower recovery boundary; pg_archivecleanup decides
# which files precede that boundary. Dry-run is the default.

set -euo pipefail

apply=false
case "${1:-}" in
  "") ;;
  --apply) apply=true ;;
  -h|--help)
    echo "usage: faas-pg-wal-prune.sh [--apply]"
    exit 0
    ;;
  *) echo "unknown argument: $1" >&2; exit 2 ;;
esac

archive_root="${FAAS_PG_WAL_ARCHIVE_ROOT:-/var/lib/pgsql/archive}"
basebackup_root="${FAAS_PG_BASEBACKUP_ROOT:-/var/lib/pgsql/basebackup}"
state_root="${FAAS_PG_BACKUP_STATE_ROOT:-/var/lib/faas/backup-state}"
rclone_remote="${OFF_HOST_BACKUP_REMOTE:-offhostbox}"
wal_path="${OFF_HOST_BACKUP_WAL_PATH:-faas-pg-wal}"
rclone_config="${FAAS_OFF_HOST_BACKUP_RCLONE_CONFIG:-/var/lib/pgsql/backup-rclone.conf}"

fail() { echo "faas-pg-wal-prune: $*" >&2; exit 1; }
for tool in find sort tar sed head pg_archivecleanup rclone mktemp basename wc tr date stat mv chmod mkdir; do
  command -v "$tool" >/dev/null 2>&1 || fail "$tool is required"
done
[[ -d "$archive_root" ]] || fail "WAL archive missing: $archive_root"
[[ -d "$basebackup_root" ]] || fail "basebackup root missing: $basebackup_root"
[[ -r "$rclone_config" ]] || fail "rclone config unreadable: $rclone_config"
grep -Fqx "[$rclone_remote]" "$rclone_config" || fail "rclone remote [$rclone_remote] is not configured"
[[ -n "$wal_path" ]] || fail "OFF_HOST_BACKUP_WAL_PATH is empty"

backups=()
while IFS= read -r backup; do
  backups+=("$backup")
done < <(find "$basebackup_root" -mindepth 1 -maxdepth 1 -type d -name 'basebackup-*' -print | sort)
((${#backups[@]} > 0)) || fail "no retained basebackup found"

# A missing manifest anywhere in the retained set makes the retention
# promise ambiguous, so abort before remote checks or deletion.
for backup in "${backups[@]}"; do
  [[ -s "$backup/base.tar.gz" ]] || fail "base archive missing: $backup/base.tar.gz"
  [[ -s "$backup/backup_manifest" ]] || fail "backup manifest missing: $backup/backup_manifest"
done

oldest="${backups[0]}"
backup_label="$(tar -xOzf "$oldest/base.tar.gz" backup_label 2>/dev/null)" || fail "cannot read backup_label from $oldest/base.tar.gz"
boundary="$(sed -nE 's/^START WAL LOCATION: .+ \(file ([0-9A-F]{24})\)$/\1/p' <<<"$backup_label" | head -n1)"
[[ "$boundary" =~ ^[0-9A-F]{24}$ ]] || fail "oldest retained backup has no valid START WAL file: $oldest"

candidate_paths="$(mktemp)"
candidate_names="$(mktemp)"
cleanup() { rm -f "$candidate_paths" "$candidate_names"; }
trap cleanup EXIT

pg_archivecleanup -n "$archive_root" "$boundary" >"$candidate_paths"
while IFS= read -r path; do
  [[ -n "$path" ]] || continue
  name="$(basename "$path")"
  [[ "$name" =~ ^[0-9A-F]{24}(\.[0-9A-F]{8}\.backup)?$ ]] || fail "unexpected cleanup candidate: $path"
  [[ "$path" == "$archive_root/$name" ]] || fail "cleanup candidate escaped archive root: $path"
  printf '%s\n' "$name" >>"$candidate_names"
done <"$candidate_paths"

count="$(wc -l <"$candidate_names" | tr -d ' ')"
if ((count == 0)); then
  echo "faas-pg-wal-prune: no WAL precedes retained boundary $boundary"
else
  # GCS and S3 expose object hashes to rclone. `check` fails if any object
  # is absent or differs; upload candidates that archive_command may have
  # missed during an outage, then complete all checks before the first unlink.
  if [[ "$apply" == true ]]; then
    rclone copy "$archive_root" "$rclone_remote:$wal_path" \
      --config="$rclone_config" \
      --files-from="$candidate_names" \
      --checkers=16 \
      --stats=0 \
      --quiet || fail "off-host WAL copy failed; local WAL left intact"
  fi
  rclone check "$archive_root" "$rclone_remote:$wal_path" \
    --config="$rclone_config" \
    --files-from="$candidate_names" \
    --one-way \
    --checkers=16 \
    --quiet || fail "off-host verification failed; local WAL left intact"

  bytes=0
  while IFS= read -r name; do
    size="$(wc -c <"$archive_root/$name" | tr -d ' ')" || fail "cannot size verified candidate: $name"
    bytes=$((bytes + size))
  done <"$candidate_names"

  if [[ "$apply" != true ]]; then
    echo "faas-pg-wal-prune: dry-run verified $count files ($bytes bytes); boundary=$boundary; oldest_backup=$(basename "$oldest")"
    exit 0
  fi

  while IFS= read -r name; do
    rm -f -- "$archive_root/$name"
  done <"$candidate_names"
  echo "faas-pg-wal-prune: removed $count verified files ($bytes bytes); boundary=$boundary; oldest_backup=$(basename "$oldest")"
fi

if [[ "$apply" == true ]]; then
  mkdir -p "$state_root"
  date -u +%FT%TZ >"$state_root/wal-prune-success"
  chmod 0644 "$state_root/wal-prune-success"

  stats_tmp="$(mktemp "$state_root/.wal-archive-stats.XXXXXX")"
  bytes=0
  oldest=0
  newest=0
  while IFS= read -r wal; do
    [[ -n "$wal" ]] || continue
    name="$(basename "$wal")"
    [[ "$name" =~ ^[0-9A-F]{24}(\.[0-9A-F]{8}\.backup)?$ ]] || continue
    read -r size mtime < <(stat -c '%s %Y' "$wal")
    bytes=$((bytes + size))
    if ((oldest == 0 || mtime < oldest)); then oldest=$mtime; fi
    if ((mtime > newest)); then newest=$mtime; fi
  done < <(find "$archive_root" -maxdepth 1 -type f -print)
  printf 'bytes=%s\noldest_timestamp_seconds=%s\nnewest_timestamp_seconds=%s\n' \
    "$bytes" "$oldest" "$newest" >"$stats_tmp"
  chmod 0644 "$stats_tmp"
  mv -f "$stats_tmp" "$state_root/wal-archive-stats"
fi
