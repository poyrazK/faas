#!/usr/bin/env bash
# Lock-and-test half of logs-e2e-run.sh: sets the acceptance environment,
# waits for the shared lock and runs logs-e2e-inner.sh with built binaries.
# Usage: logs-e2e-test.sh <run-dir>
set -uo pipefail
W="$1"
TC=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.10.linux-amd64/bin
export PATH="$TC:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
set -a
# shellcheck disable=SC1091
source /etc/faas/e2e-acceptance.env
set +a
export FAAS_BUILDER_BASE_PATH=/srv/fc/base/runner-builder-amd64.ext4
export FAAS_TEST_KERNEL=/srv/fc/base/vmlinux-6.1.134
export FAAS_TEST_FC_VERSION=1.7.0
export FAAS_STORAGE_BACKEND=local FAAS_STORAGE_ROOT=/srv/fc
ref_of() { python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("image",""))' "$1" 2>/dev/null; }
for pair in base:MINIMAL runner-node22:NODE22 runner-python312:PYTHON312; do
  name=${pair%%:*}; var=${pair##*:}
  ref=$(ref_of "/srv/fc/scans/$name-amd64.ext4.scan.json")
  case "$ref" in *@sha256:*) export "FAAS_DEPLOY_BASE_REF_$var=$ref" ;; esac
done
export FAAS_E2E_BIN_DIR="$W/bin"
export FAAS_GUEST_INIT="$W/init"
export FAAS_E2E_HELLO_SERVER_BINARY="$W/hello-server"
export DATABASE_URL="$FAAS_E2E_DATABASE_URL"
export FAAS_PUBLIC_IFACE="$(ip route show default | awk '{print $5; exit}')"
export FAAS_VMMD_RESOURCE_JOURNAL_DIR="$W/vmmd-resources"
mkdir -p "$FAAS_VMMD_RESOURCE_JOURNAL_DIR" "$W/tmp"
chmod 0700 "$FAAS_VMMD_RESOURCE_JOURNAL_DIR"
export TMPDIR="$W/tmp"
exec 8>/var/lock/faas-builder-acceptance.lock
echo "waiting for the acceptance lock $(date -u +%FT%TZ)"
deadline=$(( $(date +%s) + 43200 ))
while :; do
  if ! flock -w 43200 8; then echo "HOST_LOCK_HELD"; exit 75; fi
  src=$(findmnt -n -o SOURCE --target /srv/fc || true)
  case "$src" in
    /dev/loop*) echo "srv/fc overlaid by $src; yielding lock $(date -u +%FT%TZ)" ;;
    *) break ;;
  esac
  flock -u 8
  [ "$(date +%s)" -ge "$deadline" ] && { echo SRV_FC_STILL_OVERLAID; exit 75; }
  sleep 120
done
echo "lock acquired $(date -u +%FT%TZ); load $(cut -d' ' -f1-3 /proc/loadavg)"
if [ -d /srv/fc/jail/.native-processes ]; then
  echo "native journal present (another session's); running under a private /srv/fc/jail"
  unshare --mount --propagation private bash "$W/logs-e2e-inner.sh" "$W" private
else
  bash "$W/logs-e2e-inner.sh" "$W"
fi
echo "RUN_EXIT=$? $(date -u +%FT%TZ)"
