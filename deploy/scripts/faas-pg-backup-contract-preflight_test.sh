#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PREFLIGHT="$SCRIPT_DIR/faas-pg-backup-contract-preflight.sh"
ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

bash -n "$PREFLIGHT"
bash "$PREFLIGHT" --static "$ROOT" >/dev/null

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
BIN="$TMP/bin"
mkdir -p "$BIN"
IDENTITY_HELPER="$BIN/faas-rclone-backup-identity.py"
cat >"$BIN/uname" <<'EOF'
#!/usr/bin/env bash
printf 'Linux\n'
EOF
cat >"$BIN/systemctl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
case "$2" in
  faas-pg-basebackup-push.service) printf 'OFF_HOST_BACKUP_REMOTE=offhostbox OFF_HOST_BACKUP_BASEBACKUP_PATH=faas-pg-basebackup FAAS_RCLONE_BIN=%s\n' "${BACKUP_IDENTITY_HELPER:?}" ;;
  faas-pg-wal-prune.service) printf 'OFF_HOST_BACKUP_REMOTE=offhostbox OFF_HOST_BACKUP_WAL_PATH=faas-pg-wal FAAS_RCLONE_BIN=%s\n' "${BACKUP_IDENTITY_HELPER:?}" ;;
  *) exit 1 ;;
esac
EOF
cat >"$BIN/psql" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf "if test ! -f /var/lib/pgsql/archive/%%f; then cp %%p /var/lib/pgsql/archive/%%f; fi; %s copyto offhostbox:faas-pg-wal/%%f\n" "${BACKUP_IDENTITY_HELPER:?}"
EOF
cat >"$BIN/runuser" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
while [[ "$1" != -- ]]; do shift; done
shift
exec "$@"
EOF
chmod +x "$BIN"/*
touch "$IDENTITY_HELPER"
chmod 0755 "$IDENTITY_HELPER"

PATH="$BIN:$PATH" SYSTEMCTL_BIN="$BIN/systemctl" RUNUSER_BIN="$BIN/runuser" PSQL_BIN="$BIN/psql" BACKUP_IDENTITY_HELPER="$IDENTITY_HELPER" BACKUP_CONTRACT_PREFLIGHT_TEST_MODE=1 \
  "$PREFLIGHT" >/dev/null

cat >"$BIN/psql" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf "offhostbox:wrong-namespace/%%f\n"
EOF
if PATH="$BIN:$PATH" SYSTEMCTL_BIN="$BIN/systemctl" RUNUSER_BIN="$BIN/runuser" PSQL_BIN="$BIN/psql" BACKUP_IDENTITY_HELPER="$IDENTITY_HELPER" BACKUP_CONTRACT_PREFLIGHT_TEST_MODE=1 \
  "$PREFLIGHT" >/dev/null 2>&1; then
  echo "expected runtime namespace mismatch to fail" >&2
  exit 1
fi

echo "PASS: backup contract preflight catches static and effective namespace drift"
