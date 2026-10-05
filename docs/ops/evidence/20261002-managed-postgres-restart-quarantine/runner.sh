#!/usr/bin/env bash
set -Eeuo pipefail
root=/var/tmp/faas-metal-smoke-managed-pg-restart-quarantine-4d700a71
test "${EUID}" -eq 0
test "$(uname -m)" = x86_64
test -c /dev/kvm
test -f /etc/faas/builder-acceptance-host
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export TMPDIR=/run/mpq-4d700a71 GOFLAGS=-p=1 GOMAXPROCS=1 GOGC=50 GOMEMLIMIT=2GiB
export GOCACHE="$root/go-build"
export GOMODCACHE="$root/go-mod"
export GOPATH="$root/go-path"
export GOLANGCI_LINT_CACHE="$root/lint-cache"
export FAAS_TEST_KERNEL=/srv/fc/base/vmlinux-6.1.134
export FAAS_TEST_FC_VERSION=1.7.0 FAAS_TEST_REFERENCE_SSD=0
exec 9>/var/lock/faas-builder-acceptance.lock
flock -w 1800 9
printf '%s  %s\n' '43eb47623b2cb330d339d882ab89285e3a597bcc30bf6b993d8144c53feef6e9' "$root/source-verified.tar.gz" | sha256sum -c -
mkdir -p "$root/source"
tar -xzf "$root/source-verified.tar.gz" -C "$root/source"
cd "$root/source"
printf 'validation source: 4d700a7176e697e8a76ed4b92a45da79faee8d7d\n'
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

# Bounded Linux regressions supplement the passing full macOS race suite.
# The initial full Linux attempt exceeded the transient unit memory limit.
set +e
/usr/local/bin/go test -race -count=1 -run '^(TestRestartQuarantine|TestBuildReadinessProbe|TestGrpcBoundSignal)' ./pkg/fcvm ./cmd/vmmd > "$root/portable-linux.log" 2>&1
portable_rc=$?
set -e
printf '%s\n' "$portable_rc" > "$root/portable-linux.exit"
if [ "$portable_rc" -ne 0 ]; then exit "$portable_rc"; fi

# This bounded selection exercises ADR-398 and retained-ownership dependencies.
# It is a nested-node diagnostic, not the unfiltered native release gate.
export RUN_REGEX='^(TestMetalRestartQuarantine|TestRestartQuarantine|TestMetalFailureOutbox|TestMetalTeardownRetainsOwnership|TestFailureReport|TestRecoveredFailureReport)'
set +e
make GO=/usr/local/bin/go PKGS='./pkg/fcvm ./cmd/vmmd' RUN_ARGS='-timeout=20m -v' test-metal \
  2>&1 | tee "$root/metal.log"
metal_rc=${PIPESTATUS[0]}
set -e
printf '%s\n' "$metal_rc" > "$root/metal.exit"
make leakcheck
set +e
/usr/local/bin/go tool golangci-lint run --new-from-patch="$root/change-final.patch" ./pkg/fcvm ./cmd/vmmd > "$root/linux-lint.log" 2>&1
lint_rc=$?
set -e
printf '%s\n' "$lint_rc" > "$root/linux-lint.exit"
printf 'diagnostic exit: metal=%s lint=%s\n' "$metal_rc" "$lint_rc"
if [ "$metal_rc" -ne 0 ]; then exit "$metal_rc"; fi
exit "$lint_rc"
