#!/usr/bin/env bash
# Seal the control plane's host-local state (ADR-1344 §8) to the operator's
# age recipients and push it off-host, then verify the remote copy before
# pruning local bundles. The host can never read its own backup.
set -euo pipefail

recipients="${FAAS_CP_STATE_BACKUP_RECIPIENTS:-/etc/faas/cp-state-backup.recipients}"
spec="${FAAS_CP_STATE_BACKUP_PATHS:-/etc/faas/cp-state-backup.paths}"
out_dir="${FAAS_CP_STATE_BACKUP_DIR:-/var/lib/faas/cp-state-backup}"
config="${FAAS_OFF_HOST_BACKUP_RCLONE_CONFIG:-/etc/faas/secrets/storage-box/rclone.conf}"
remote="${OFF_HOST_BACKUP_REMOTE:-offhostbox}"
path="${FAAS_CP_STATE_BACKUP_PATH:-faas-cp-state}"
keep="${FAAS_CP_STATE_BACKUP_KEEP:-3}"
rclone_bin="${FAAS_RCLONE_BIN:-rclone}"
gregalectl="${FAAS_GREGALECTL:-/usr/local/bin/gregalectl}"
textfile="${FAAS_CP_STATE_BACKUP_TEXTFILE:-/var/lib/node_exporter/textfile_collector/faas_cp_state_backup.prom}"
host="${FAAS_CP_STATE_BACKUP_HOST:-$(hostname -s)}"

fail() { echo "faas-cp-state-backup: $*" >&2; exit 1; }
[[ -s "$recipients" ]] || { echo "faas-cp-state-backup: disabled: no operator recipients configured" >&2; exit 0; }
[[ -r "$config" ]] || fail "off-host credential not installed: $config"
grep -Fqx "[$remote]" "$config" || fail "rclone remote $remote is not configured in $config"
[[ "$keep" =~ ^[1-9][0-9]*$ ]] || fail "FAAS_CP_STATE_BACKUP_KEEP must be >= 1"

mkdir -p "$out_dir"
chmod 0700 "$out_dir"
name="cp-state-$(date -u +%Y%m%dT%H%M%SZ).tar.age"
"$gregalectl" backup seal-cp-state --paths "$spec" --recipients "$recipients" --out "$out_dir/$name" --primary "$host"

"$rclone_bin" copy "$out_dir/$name" "$remote:$path/$host/" --config="$config" --stats=0 --quiet
"$rclone_bin" check "$out_dir" "$remote:$path/$host/" --include "$name" --config="$config" --one-way --quiet \
  || fail "off-host verification of $name failed; local bundles left intact"

bundles=()
while IFS= read -r bundle; do
  bundles+=("$bundle")
done < <(find "$out_dir" -maxdepth 1 -type f -name 'cp-state-*.tar.age' -print | sort)
remove_count=$((${#bundles[@]} - keep))
for ((i = 0; i < remove_count; i++)); do
  rm -f -- "${bundles[$i]}"
done

size="$(wc -c <"$out_dir/$name" | tr -d ' ')"
tmp="${textfile}.tmp.$$"
cat >"$tmp" <<EOF
# HELP faas_cp_state_backup_last_success_timestamp_seconds Unix time of the last control-plane state backup verified off-host (ADR-1344 §8).
# TYPE faas_cp_state_backup_last_success_timestamp_seconds gauge
faas_cp_state_backup_last_success_timestamp_seconds $(date +%s)
# HELP faas_cp_state_backup_bytes Size of the last control-plane state bundle pushed off-host.
# TYPE faas_cp_state_backup_bytes gauge
faas_cp_state_backup_bytes $size
EOF
chmod 0644 "$tmp"
mv "$tmp" "$textfile"
echo "faas-cp-state-backup: pushed and verified $name (${size} bytes) at $remote:$path/$host/"
