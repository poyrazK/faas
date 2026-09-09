#!/usr/bin/env bash
# Copy every completed basebackup off-host, verify the remote bytes, then
# keep only the newest local recovery points. The remote backend owns its
# longer lifecycle; local deletion never propagates back to it.

set -euo pipefail

src="${FAAS_PG_BASEBACKUP_ROOT:-/var/lib/pgsql/basebackup}"
state_root="${FAAS_PG_BACKUP_STATE_ROOT:-/var/lib/faas/backup-state}"
config="${FAAS_OFF_HOST_BACKUP_RCLONE_CONFIG:-/etc/faas/secrets/storage-box/rclone.conf}"
remote="${OFF_HOST_BACKUP_REMOTE:-offhostbox}"
path="${OFF_HOST_BACKUP_BASEBACKUP_PATH:-faas-pg-basebackup}"
keep="${FAAS_PG_LOCAL_BASEBACKUP_KEEP:-2}"

fail() { echo "faas-pg-basebackup-push: $*" >&2; exit 1; }
[[ -r "$config" ]] || { echo "off-host backup disabled: credential not installed" >&2; exit 0; }
grep -Fqx "[$remote]" "$config" || { echo "off-host backup disabled: $remote remote not configured" >&2; exit 0; }
[[ -d "$src" ]] || fail "basebackup root missing: $src"
[[ "$keep" =~ ^[1-9][0-9]*$ ]] || fail "FAAS_PG_LOCAL_BASEBACKUP_KEEP must be >= 1"

backups=()
while IFS= read -r backup; do
  backups+=("$backup")
done < <(find "$src" -mindepth 1 -maxdepth 1 -type d -name 'basebackup-*' -print | sort)
((${#backups[@]} > 0)) || fail "$src has no completed basebackup"
for backup in "${backups[@]}"; do
  [[ -s "$backup/base.tar.gz" ]] || fail "base archive missing: $backup/base.tar.gz"
  [[ -s "$backup/backup_manifest" ]] || fail "backup manifest missing: $backup/backup_manifest"
done

rclone copy "$src" "$remote:$path" --config="$config" --stats=0 --quiet
rclone check "$src" "$remote:$path" --config="$config" --one-way --checkers=16 --quiet \
  || fail "off-host verification failed; local basebackups left intact"

remove_count=$((${#backups[@]} - keep))
if ((remove_count > 0)); then
  for ((i = 0; i < remove_count; i++)); do
    rm -rf -- "${backups[$i]}"
  done
else
  remove_count=0
fi

mkdir -p "$state_root"
date -u +%FT%TZ >"$state_root/basebackup-push-success"
chmod 0644 "$state_root/basebackup-push-success"
echo "faas-pg-basebackup-push: verified ${#backups[@]} backup(s) off-host; pruned $remove_count local backup(s)"
