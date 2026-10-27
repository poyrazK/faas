#!/bin/bash
# Hunt 14 H14-59 metal evidence on gregale-internal-test-1: a worker's stop
# signal reaches the workload through guest-init. Shared acceptance lock,
# private tmpfs jail, leakcheck before and after. Mirrors
# docs/ops/evidence/20261025-guest-disk-mounts/guest-disk-mount-metal.sh.
set -uo pipefail
W=/mnt/e2e-scratch/workerstop-20261025; R=/mnt/e2e-scratch/restore350-20261021
BRANCH=${BRANCH:-claude/hunt14-worker-stop-signal}
mkdir -p $W/bin $W/images $W/tmp $W/logs
# Reuse an existing build cache on this host instead of growing a new one.
export GOCACHE=/mnt/e2e-scratch/guestdisk-20261025/gocache GOMODCACHE=/mnt/e2e-scratch/guestdisk-20261025/gomod GOFLAGS=-buildvcs=false GOTOOLCHAIN=auto
log() { echo "[$(date -u +%H:%M:%S)] $*"; }
rm -rf $W/src
git clone -q --depth 1 --branch "$BRANCH" https://github.com/poyrazK/faas $W/src || exit 1
cd $W/src
log "source $(git rev-parse --short HEAD)"
go test -c -tags metal -o $W/bin/fcvm.test ./pkg/fcvm || exit 1
CGO_ENABLED=0 go build -o $W/bin/vmmd ./cmd/vmmd || exit 1
CGO_ENABLED=0 go build -o $W/bin/vmmd-jail-helper ./cmd/vmmd-jail-helper || exit 1
CGO_ENABLED=0 go build -o $W/bin/guest-init ./guest/init || exit 1
rm -f $W/images/*.ext4; bash $R/build-images.sh $W/bin/guest-init $W/images >/dev/null || exit 1
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
# The new worker stop gate, a worker boot that must stay up until stopped,
# and an HTTP app's stop path, which still signals the VMM.
TESTS='^(TestMetalWorkerStopSignalReachesTheWorkload|TestMetalWorker_NotReapedAfterIdleWindow)$'
# Mounts another run leaked under /srv/fc/jail are lazily unmounted in this
# private namespace only, so in-test leakchecks see this run alone; the
# host-level leakchecks above and below still list them.
unshare --mount --propagation private sh -c 'for m in $(awk "\$5 ~ \"^/srv/fc/jail/\" {print \$5}" /proc/self/mountinfo | sort -r); do umount -l "$m"; done; mount -t tmpfs -o size=6g,mode=0755 tmpfs /srv/fc/jail && exec "$0" "$@"' \
  $W/bin/fcvm.test -test.run "$TESTS" -test.v -test.count=1 -test.timeout 30m > $W/logs/fcvm.log 2>&1
log "fcvm: rc=$? $(grep -c '^--- PASS' $W/logs/fcvm.log) pass $(grep -c '^--- FAIL' $W/logs/fcvm.log) fail $(grep -c '^--- SKIP' $W/logs/fcvm.log) skip"
bash $W/src/deploy/scripts/leakcheck.sh > $W/logs/leakcheck-after.log 2>&1; log "leakcheck after: rc=$? $(tail -1 $W/logs/leakcheck-after.log)"
flock -u 9
cd $W/src && GOMEMLIMIT=3GiB go test -p 1 -count=1 -run 'TestLivenessPortDeliversWorkerStopSignals|TestControlPortDeliversWorkerStopSignals' ./guest/init > $W/logs/guest-init-unit.log 2>&1
log "guest-init unit: rc=$? $(tail -1 $W/logs/guest-init-unit.log)"
grep -E "^(=== RUN|--- |    [a-z_]+_test.go:)" $W/logs/fcvm.log | head -60; diff <(grep LEAK $W/logs/leakcheck-before.log) <(grep LEAK $W/logs/leakcheck-after.log) && log "no new leaks"
log "done"
