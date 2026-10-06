#!/usr/bin/env bash
set -Eeuo pipefail
root=/var/tmp/faas-metal-smoke-managed-pg-failure-reports-d598bb27
test "${EUID}" -eq 0
test "$(uname -m)" = x86_64
test -c /dev/kvm
test -f /etc/faas/builder-acceptance-host
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export TMPDIR=/run/mpgr-d598bb27 GOFLAGS=-p=1 GOMAXPROCS=2
export GOCACHE="$root/go-build"
export GOMODCACHE="$root/go-mod"
export GOPATH="$root/go-path"
export FAAS_TEST_KERNEL=/srv/fc/base/vmlinux-6.1.134
export FAAS_TEST_FC_VERSION=1.7.0 FAAS_TEST_REFERENCE_SSD=0
exec 9>/var/lock/faas-builder-acceptance.lock
flock -w 1800 9
printf '%s  %s\n' 'fcb6804f9168578468cf7ea5c5aabca7f28fd53a45e4ef970cfde59033be1d9c' "$root/source-selected.tar.gz" | sha256sum -c -
mkdir -p "$root/source"
tar -xzf "$root/source-selected.tar.gz" -C "$root/source"
printf '%s  %s\n' '09876fa7df2950b0defdd2c7dc4543577d5b6d7d3b1f9bf473ecc2d4cab55f6c' "$root/source-supplement.tar.gz" | sha256sum -c -
tar -xzf "$root/source-supplement.tar.gz" -C "$root/source"
printf '%s  %s\n' 'ef12629e6d4edf7fc53c8960415bd4224e7f7044ee8952b1a028443a051c552f' "$root/source-node-supplement.tar.gz" | sha256sum -c -
tar -xzf "$root/source-node-supplement.tar.gz" -C "$root/source"
cd "$root/source"
printf 'validation source: 040ed52d0e0d2d8609a6c8c590454704a543803a\n'
uname -a
/usr/local/bin/go version
for service in faas-vmmd.service faas-builderd.service faas-imaged.service faas-gatewayd-internal.service; do
  if systemctl is-active --quiet "$service"; then
    printf 'refusing diagnostic run with active service: %s\n' "$service" >&2
    exit 1
  fi
done
make leakcheck
cleanup() {
  local rc=$?
  trap - EXIT
  if ! make -C "$root/source" leakcheck; then rc=1; fi
  exit "$rc"
}
trap cleanup EXIT

# Match the stock native metal runner's immutable two-drive hello fixtures.
# guest/init is identical between the baseline and validation commits.
base_skeleton="$root/fixtures/base-skeleton"
layer_skeleton="$root/fixtures/layer-skeleton"
mkdir -p "$base_skeleton/bin" "$base_skeleton/sbin" "$base_skeleton/etc/faas" \
  "$layer_skeleton/upper/etc/faas" "$layer_skeleton/upper/tmp"
for directory in dev overlay proc run sys sys/fs/cgroup tmp; do
  mkdir -p "$base_skeleton/$directory"
done
CGO_ENABLED=0 /usr/local/bin/go build -trimpath -buildvcs=false -tags linux \
  -o "$base_skeleton/sbin/init" ./guest/init
install -m 0755 /usr/bin/busybox "$base_skeleton/bin/busybox"
for name in bin/sh bin/ash bin/cat; do
  ln -sfn /bin/busybox "$base_skeleton/$name"
done
printf '%s\n' \
  '{"entrypoint":["/bin/busybox","httpd","-f","-p","8080","-h","/"],"port":8080}' \
  > "$layer_skeleton/upper/etc/faas/app.json"
export FAAS_TEST_BASE_ROOTFS="$root/fixtures/hello-base.ext4"
export FAAS_TEST_LAYER_ROOTFS="$root/fixtures/hello-layer.ext4"
truncate -s 64M "$FAAS_TEST_BASE_ROOTFS"
mkfs.ext4 -q -O '^has_journal' -d "$base_skeleton" -L faas-metal-smoke -F "$FAAS_TEST_BASE_ROOTFS"
truncate -s 16M "$FAAS_TEST_LAYER_ROOTFS"
mkfs.ext4 -q -O '^has_journal' -d "$layer_skeleton" -L faas-metal-layer -F "$FAAS_TEST_LAYER_ROOTFS"
e2fsck -fn "$FAAS_TEST_BASE_ROOTFS"
e2fsck -fn "$FAAS_TEST_LAYER_ROOTFS"
chmod 0644 "$FAAS_TEST_BASE_ROOTFS" "$FAAS_TEST_LAYER_ROOTFS"
sha256sum "$FAAS_TEST_KERNEL" "$FAAS_TEST_BASE_ROOTFS" "$FAAS_TEST_LAYER_ROOTFS"

# This bounded selection exercises ADR-397 and its teardown-accounting dependencies.
# It is a nested-node diagnostic, not the unfiltered native release gate.
export RUN_REGEX='^(TestMetalFailureOutbox|TestMetalSchedulerRetainsAccounting|TestMetalTeardownRetainsOwnership|TestOutbox|TestFailureReport|TestRecoveredFailureReport|TestReportLivenessFailed|TestReportWorkloadOOM|TestEngineTeardown|TestLiveness_DestroyTimeout)'
set +e
make GO=/usr/local/bin/go PKGS='./pkg/fcvm ./pkg/sched ./pkg/vmmd/failureoutbox ./pkg/scheddgrpc ./cmd/vmmd' RUN_ARGS='-timeout=20m -v' test-metal \
  2>&1 | tee "$root/metal.log"
metal_rc=${PIPESTATUS[0]}
set -e
printf '%s\n' "$metal_rc" > "$root/metal.exit"
make leakcheck
printf 'diagnostic exit: metal=%s\n' "$metal_rc"
exit "$metal_rc"
