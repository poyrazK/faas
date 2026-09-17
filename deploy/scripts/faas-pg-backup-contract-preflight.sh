#!/usr/bin/env bash
# Verify that every PostgreSQL off-host backup producer and consumer resolves
# to one provider-neutral namespace. Runtime mode is read-only and is intended
# for a deployed control plane; --static audits the checked-in sources.
set -euo pipefail

REMOTE="${OFF_HOST_BACKUP_REMOTE:-offhostbox}"
BASEBACKUP_PATH="${OFF_HOST_BACKUP_BASEBACKUP_PATH:-faas-pg-basebackup}"
WAL_PATH="${OFF_HOST_BACKUP_WAL_PATH:-faas-pg-wal}"
SYSTEMCTL_BIN="${SYSTEMCTL_BIN:-systemctl}"
RUNUSER_BIN="${RUNUSER_BIN:-runuser}"
PSQL_BIN="${PSQL_BIN:-psql}"
BACKUP_IDENTITY_HELPER="${BACKUP_IDENTITY_HELPER:-/usr/local/lib/faas/faas-rclone-backup-identity.py}"

usage() {
  cat <<'EOF'
usage: faas-pg-backup-contract-preflight.sh [--static [repo-root]]

Without arguments, verify the effective systemd and PostgreSQL runtime paths.
--static verifies the checked-in restore, archive, prune, push, and unit
sources without contacting a host or an object store.
EOF
}

fail() { printf 'backup contract: FAIL: %s\n' "$*" >&2; exit 1; }
ok() { printf 'backup contract: OK: %s\n' "$*"; }

static_check() {
  local root="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
  local vars="$root/deploy/ansible/group_vars/control_plane/off_host_backup.yml"
  local postgres="$root/deploy/ansible/roles/postgres/tasks/main.yml"
  local restore="$root/deploy/scripts/pg-restore-verify.sh"
  local push="$root/deploy/scripts/faas-pg-basebackup-push.sh"
  local prune="$root/deploy/scripts/faas-pg-wal-prune.sh"
  local push_unit="$root/deploy/systemd/faas-pg-basebackup-push.service"
  local prune_unit="$root/deploy/systemd/faas-pg-wal-prune.service"
  local identity_helper="$root/deploy/scripts/faas-rclone-backup-identity.py"

  for file in "$vars" "$postgres" "$restore" "$push" "$prune" "$push_unit" "$prune_unit" "$identity_helper"; do
    [[ -f "$file" ]] || fail "missing source: $file"
  done

  grep -Fqx 'off_host_backup_remote: "offhostbox"' "$vars" \
    || fail "Ansible remote alias is not offhostbox"
  grep -Fqx 'off_host_backup_basebackup_path: "faas-pg-basebackup"' "$vars" \
    || fail "Ansible basebackup path is not faas-pg-basebackup"
  grep -Fqx 'off_host_backup_wal_path: "faas-pg-wal"' "$vars" \
    || fail "Ansible WAL path is not faas-pg-wal"

  grep -Fq 'RCLONE_REMOTE:-offhostbox' "$restore" \
    || fail "restore verify does not default to offhostbox"
  grep -Fq 'BASEBACKUP_PATH:-faas-pg-basebackup' "$restore" \
    || fail "restore verify does not default to faas-pg-basebackup"
  grep -Fq 'WAL_PATH:-faas-pg-wal' "$restore" \
    || fail "restore verify does not default to faas-pg-wal"
  grep -Fq 'OFF_HOST_BACKUP_REMOTE:-offhostbox' "$push" \
    || fail "basebackup push does not default to offhostbox"
  grep -Fq 'OFF_HOST_BACKUP_WAL_PATH:-faas-pg-wal' "$prune" \
    || fail "WAL prune does not default to faas-pg-wal"

  grep -Fq 'Environment=OFF_HOST_BACKUP_REMOTE=offhostbox' "$push_unit" \
    || fail "basebackup push unit remote drift"
  grep -Fq 'Environment=OFF_HOST_BACKUP_BASEBACKUP_PATH=faas-pg-basebackup' "$push_unit" \
    || fail "basebackup push unit path drift"
  grep -Fq 'Environment=OFF_HOST_BACKUP_REMOTE=offhostbox' "$prune_unit" \
    || fail "WAL prune unit remote drift"
  grep -Fq 'Environment=OFF_HOST_BACKUP_WAL_PATH=faas-pg-wal' "$prune_unit" \
    || fail "WAL prune unit path drift"
  grep -Fq 'Environment=FAAS_RCLONE_BIN=/usr/local/lib/faas/faas-rclone-backup-identity.py' "$push_unit" \
    || fail "basebackup push does not use the keyless backup identity helper"
  grep -Fq 'Environment=FAAS_RCLONE_BIN=/usr/local/lib/faas/faas-rclone-backup-identity.py' "$prune_unit" \
    || fail "WAL prune does not use the keyless backup identity helper"
  grep -Fq 'off_host_backup_wal_path' "$postgres" \
    || fail "PostgreSQL archive command is not tied to the canonical WAL variable"
  grep -Fq 'ALTER SYSTEM RESET archive_command' "$postgres" \
    || fail "PostgreSQL role does not clear stale ALTER SYSTEM archive_command overrides"

  if grep -Fq 'gregale-pg-backups' "$postgres" "$restore" "$push" "$prune"; then
    fail "provider-specific bucket namespace found in a backup producer/consumer"
  fi
  ok "all backup producers and consumers use ${REMOTE}:${BASEBACKUP_PATH} and ${REMOTE}:${WAL_PATH}"
}

runtime_check() {
  if [[ "${BACKUP_CONTRACT_PREFLIGHT_TEST_MODE:-0}" != 1 ]]; then
    [[ "$(uname -s)" == Linux ]] || fail "runtime preflight must run on Linux"
    [[ $EUID -eq 0 ]] || fail "runtime preflight must run as root"
  fi

  local base_env wal_env archive_command expected
  [[ -x "$BACKUP_IDENTITY_HELPER" ]] \
    || fail "keyless backup identity helper is missing or not executable: ${BACKUP_IDENTITY_HELPER}"
  base_env="$("$SYSTEMCTL_BIN" show faas-pg-basebackup-push.service -p Environment --value 2>/dev/null)" \
    || fail "cannot read faas-pg-basebackup-push.service environment"
  wal_env="$("$SYSTEMCTL_BIN" show faas-pg-wal-prune.service -p Environment --value 2>/dev/null)" \
    || fail "cannot read faas-pg-wal-prune.service environment"
  [[ "$base_env" == *"OFF_HOST_BACKUP_REMOTE=${REMOTE}"* ]] \
    || fail "basebackup push remote differs from ${REMOTE}"
  [[ "$base_env" == *"OFF_HOST_BACKUP_BASEBACKUP_PATH=${BASEBACKUP_PATH}"* ]] \
    || fail "basebackup push path differs from ${BASEBACKUP_PATH}"
  [[ "$wal_env" == *"OFF_HOST_BACKUP_REMOTE=${REMOTE}"* ]] \
    || fail "WAL prune remote differs from ${REMOTE}"
  [[ "$wal_env" == *"OFF_HOST_BACKUP_WAL_PATH=${WAL_PATH}"* ]] \
    || fail "WAL prune path differs from ${WAL_PATH}"
  [[ "$base_env" == *"FAAS_RCLONE_BIN=${BACKUP_IDENTITY_HELPER}"* ]] \
    || fail "basebackup push does not use ${BACKUP_IDENTITY_HELPER}"
  [[ "$wal_env" == *"FAAS_RCLONE_BIN=${BACKUP_IDENTITY_HELPER}"* ]] \
    || fail "WAL prune does not use ${BACKUP_IDENTITY_HELPER}"

  archive_command="$($RUNUSER_BIN -u postgres -- "$PSQL_BIN" -X -A -t -c 'SHOW archive_command' 2>/dev/null | tr -d '\r\n')" \
    || fail "cannot read effective PostgreSQL archive_command"
  expected="${REMOTE}:${WAL_PATH}/%f"
  [[ "$archive_command" == *"/var/lib/pgsql/archive/%f"* ]] \
    || fail "archive_command is missing the local authoritative archive"
  [[ "$archive_command" == *"$expected"* ]] \
    || fail "archive_command does not target ${expected}"
  [[ "$archive_command" == *"$BACKUP_IDENTITY_HELPER"* ]] \
    || fail "archive_command does not use ${BACKUP_IDENTITY_HELPER}"
  ok "effective PostgreSQL archive_command targets ${expected}"
  ok "systemd push/prune environments agree with the canonical namespace"
}

case "${1:-}" in
  --help|-h) usage ;;
  --static)
    [[ $# -le 2 ]] || { usage >&2; exit 2; }
    static_check "${2:-}"
    ;;
  "") runtime_check ;;
  *) usage >&2; exit 2 ;;
esac
