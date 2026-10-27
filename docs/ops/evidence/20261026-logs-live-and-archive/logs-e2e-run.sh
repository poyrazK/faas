#!/usr/bin/env bash
# TestLogsLiveAndArchivedMetal on gregale-internal-test-1 (detached; waits for
# the shared acceptance lock). Builds on the scratch disk, then runs under the
# lock with leakcheck before and after. Mirrors the release-guards drill
# runner: snapshots stay on the scratch disk and only VMs this run created are
# removed.
# Usage: sudo -b setsid nohup bash logs-e2e-run.sh <run-dir> <branch> >/dev/null 2>&1 </dev/null
set -uo pipefail
W="$1"
BRANCH="$2"
mkdir -p "$W/bin" "$W/tmp" "$W/vmmd-resources"
exec >>"$W/run.log" 2>&1
echo "start $(date -u +%FT%TZ)"
TC=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.10.linux-amd64/bin
idle() { ! pgrep -f '(^| )go test |/pkg/tool/linux_amd64/compile ' >/dev/null; }
(
  export PATH="$TC:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
  export GOTOOLCHAIN=local GOMODCACHE=/mnt/e2e-scratch/sr-gomod GOCACHE="$W/go-build" GOTMPDIR="$W/tmp"
  export GOGC=25 GOMEMLIMIT=4GiB GOMAXPROCS=2 GOFLAGS=-modcacherw
  [ -d "$W/src" ] || git clone -q --depth 1 --branch "$BRANCH" https://github.com/poyrazK/faas "$W/src" || { echo CLONE_FAILED; exit 1; }
  cd "$W/src" || exit 1
  echo "revision $(git rev-parse HEAD)"
  for _ in $(seq 1 360); do idle && break; sleep 20; done
  go mod download || { echo MOD_DOWNLOAD_FAILED; exit 1; }
  ok=1
  for d in apid schedd vmmd imaged gatewayd-internal gatewayd-public meterd builderd \
    vmmd-stream-bridge vmmd-raw-bridge vmmd-tcp-bridge vmmd-udp-bridge; do
    CGO_ENABLED=0 nice go build -p 1 -trimpath -tags metal -ldflags "-s -w" -o "$W/bin/$d" "./cmd/$d" || { echo "BUILD_FAILED $d"; ok=0; }
  done
  CGO_ENABLED=0 nice go build -p 1 -trimpath -ldflags "-s -w" -o "$W/bin/vmmd-jail-helper" ./cmd/vmmd-jail-helper || { echo "BUILD_FAILED vmmd-jail-helper"; ok=0; }
  CGO_ENABLED=0 nice go build -p 1 -trimpath -buildvcs=false -o "$W/init" ./guest/init || { echo "BUILD_FAILED init"; ok=0; }
  (cd pkg/e2etest/testdata/helloserver && CGO_ENABLED=0 nice go build -trimpath -ldflags "-s -w" -o "$W/hello-server" .) || { echo "BUILD_FAILED hello-server"; ok=0; }
  CGO_ENABLED=0 nice go test -c -vet=off -p 1 -trimpath -tags metal -ldflags "-s -w" -o "$W/e2e.test" ./cmd/e2e || { echo "BUILD_FAILED e2e.test"; ok=0; }
  [ "$ok" = 1 ] || { echo BUILD_FAILED; exit 1; }
  echo "BUILD_OK $(date -u +%FT%TZ)"
)
rc=$?
rm -rf "$W/go-build"
[ "$rc" = 0 ] || { echo "STOPPED_BEFORE_TEST rc=$rc"; exit 1; }
cp "$W"/src/deploy/scripts/leakcheck* "$W/"

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
  echo "sidecar $name -> $ref"
  case "$ref" in *@sha256:*) export "FAAS_DEPLOY_BASE_REF_$var=$ref" ;; esac
done
export FAAS_E2E_BIN_DIR="$W/bin"
export FAAS_GUEST_INIT="$W/init"
export FAAS_E2E_HELLO_SERVER_BINARY="$W/hello-server"
export DATABASE_URL="$FAAS_E2E_DATABASE_URL"
export FAAS_PUBLIC_IFACE="$(ip route show default | awk '{print $5; exit}')"
export FAAS_VMMD_RESOURCE_JOURNAL_DIR="$W/vmmd-resources"
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
