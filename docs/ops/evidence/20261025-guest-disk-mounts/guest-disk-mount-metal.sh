#!/bin/bash
# #5323 metal evidence on gregale-internal-test-1: the guest-disk mount
# hardening (guestDiskMountArgs; errors=remount-ro on native loop mounts)
# under the shared acceptance lock, with leakcheck before and after.
# Mirrors docs/ops/evidence/20261025-guest-egress-shaping/egress-metal.sh.
set -uo pipefail
W=/mnt/e2e-scratch/guestdisk-20261025; R=/mnt/e2e-scratch/restore350-20261021
BRANCH=${BRANCH:-test/vmmd-guest-disk-mount-metal}
mkdir -p $W/bin $W/images $W/tmp $W/logs
export GOCACHE=$W/gocache GOMODCACHE=$W/gomod GOFLAGS=-buildvcs=false GOTOOLCHAIN=auto
log() { echo "[$(date -u +%H:%M:%S)] $*"; }
[ -d $W/src ] || git clone -q --depth 1 --branch "$BRANCH" https://github.com/poyrazK/faas $W/src || exit 1
cd $W/src
log "source $(git rev-parse --short HEAD)"
[ -x $W/bin/fcvm.test ] || go test -c -tags metal -o $W/bin/fcvm.test ./pkg/fcvm || exit 1
[ -x $W/bin/vmmd ] || CGO_ENABLED=0 go build -o $W/bin/vmmd ./cmd/vmmd || exit 1
[ -x $W/bin/vmmd-jail-helper ] || CGO_ENABLED=0 go build -o $W/bin/vmmd-jail-helper ./cmd/vmmd-jail-helper || exit 1
[ -x $W/bin/guest-init ] || CGO_ENABLED=0 go build -o $W/bin/guest-init ./guest/init || exit 1
[ -f $W/images/base.ext4 ] || bash $R/build-images.sh $W/bin/guest-init $W/images || exit 1
log "built; waiting for /run/lock/faas-builder-acceptance.lock"
exec 9>/run/lock/faas-builder-acceptance.lock
flock -w 14400 9 || { log "lock timeout"; exit 1; }
log "lock held"
export PATH=$R/bin:$PATH TMPDIR=$W/tmp
export FAAS_TEST_KERNEL=$R/bin/vmlinux FAAS_TEST_GUEST_INIT_BIN=$W/bin/guest-init
export FAAS_TEST_BASE_ROOTFS=$W/images/base.ext4 FAAS_TEST_LAYER_ROOTFS=$W/images/layer.ext4
export FAAS_TEST_VMMD_BINARY=$W/bin/vmmd FAAS_TEST_VMMD_HELPER=$W/bin/vmmd-jail-helper
bash $W/src/deploy/scripts/leakcheck.sh > $W/logs/leakcheck-before.log 2>&1; log "leakcheck before: rc=$? $(tail -1 $W/logs/leakcheck-before.log)"
cd $W/src/pkg/fcvm
# The new mount-option check, then the suites that drive the changed mounts:
# pre-boot staging on restore (loop_mount.go), secrets staged before boot,
# native loop mounts (native_loop_mounts_linux.go) and adoption after restart.
TESTS='^(TestMetalGuestDiskMountOptionsOverrideTheGuestSuperblock|TestMetalRestoreSkipsUnchangedPreBootFiles|TestMetalRestoreLearnsReusedCaptureAfterRestart|TestMetalRestoreLearnsReusedCaptureWithAppEnv|TestMetalSecretsEnvReachesGuest|TestMetalNativeLoopMountRecovery|TestMetalAdoptionKinds)$'
unshare --mount --propagation private sh -c 'mount -t tmpfs -o size=6g,mode=0755 tmpfs /srv/fc/jail && exec "$0" "$@"' \
  $W/bin/fcvm.test -test.run "$TESTS" -test.v -test.count=1 -test.timeout 45m > $W/logs/fcvm.log 2>&1
log "fcvm: rc=$? $(grep -c '^--- PASS' $W/logs/fcvm.log) pass $(grep -c '^--- FAIL' $W/logs/fcvm.log) fail $(grep -c '^--- SKIP' $W/logs/fcvm.log) skip"
bash $W/src/deploy/scripts/leakcheck.sh > $W/logs/leakcheck-after.log 2>&1; log "leakcheck after: rc=$? $(tail -1 $W/logs/leakcheck-after.log)"
flock -u 9
log "done"
