#!/usr/bin/env bash
set -Eeuo pipefail
root=/var/tmp/faas-metal-smoke-managed-pg-teardown-c3c5d7ca
test "${EUID}" -eq 0
test "$(uname -m)" = x86_64
test -c /dev/kvm
test -f /etc/faas/builder-acceptance-host
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export TMPDIR=/run/mpg-c3c5d7ca GOFLAGS=-p=1 GOMAXPROCS=2
export GOCACHE=/var/cache/faas-metal-smoke/go-build
export GOMODCACHE=/var/cache/faas-metal-smoke/go-mod
export GOPATH=/var/cache/faas-metal-smoke
export FAAS_TEST_KERNEL=/srv/fc/base/vmlinux-6.1.134
export FAAS_TEST_FC_VERSION=1.7.0 FAAS_TEST_REFERENCE_SSD=0
exec 9>/var/lock/faas-builder-acceptance.lock
flock -w 1800 9
cd "$root/source"
printf 'validation source: c3c5d7caa4dae3de5a56388eeea2c4afcd123568\n'
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
  ln -s /bin/busybox "$base_skeleton/$name"
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

# This is a broad internal diagnostic, not the native release gate. The
# two-vCPU nested node's concurrent-capacity failure was already reproduced on
# ADR-393's baseline; its evidence remains in the ADR-394 evidence bundle.
set +e
make GO=/usr/local/bin/go PKGS=./pkg/fcvm \
  RUN_ARGS='-timeout=20m -v -skip=^TestMetalBoot50Concurrent$' test-metal \
  2>&1 | tee "$root/package.log"
package_rc=${PIPESTATUS[0]}
set -e
printf '%s\n' "$package_rc" > "$root/package.exit"
make leakcheck

/usr/local/bin/go test -c -tags metal -race -o "$root/fcvm-metal.test" ./pkg/fcvm
batch_tests='^(TestMetalImageBindMount|TestMetalIPSetupBatch|TestMetalFreshNetworkPolicy|TestMetalReusedLeaseNeighbor|TestMetalPreparedBridgeMAC|TestMetalPreparedNetworkOwnership)$'
set +e
FAAS_TEST_NETWORK_BATCH=1 \
  unshare --mount --net --propagation private -- \
  sh -c 'ip link set lo up 2>/dev/null; mount -t tmpfs tmpfs /run/netns && exec "$0" -test.run "$1" -test.timeout=10m -test.v' \
  "$root/fcvm-metal.test" "$batch_tests" 2>&1 | tee "$root/namespace.log"
batch_rc=${PIPESTATUS[0]}
set -e
printf '%s\n' "$batch_rc" > "$root/namespace.exit"
make leakcheck
printf 'diagnostic exits: package=%s namespace=%s\n' "$package_rc" "$batch_rc"
if [[ "$package_rc" -ne 0 || "$batch_rc" -ne 0 ]]; then
  exit 1
fi
