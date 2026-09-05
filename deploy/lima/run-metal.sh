#!/bin/bash
# run-metal.sh — run the //go:build metal tests inside the faas-metal Lima guest.
#
# Firecracker's jailer needs root, and the metal tests resolve their kernel and
# rootfs images from FAAS_TEST_* env vars (see pkg/fcvm/manager_metal_test.go).
# This wires those to the aarch64 assets the Lima provisioner staged, then runs
# the tests. Run it from your repo checkout inside the guest:
#
#   sudo -E env "PATH=$PATH" ./deploy/lima/run-metal.sh                 # M0 gate
#   sudo -E env "PATH=$PATH" ./deploy/lima/run-metal.sh -run TestMetal  # all metal
#   sudo -E env "PATH=$PATH" RUN_TARGET=./cmd/e2e/ \
#     ./deploy/lima/run-metal.sh -run 'TestDeployWakeMetal/deploy-then-parked'  # M5 cold-boot
#
# RUN_TARGET selects the Go package passed to `go test`. Default is
# ./pkg/fcvm/ (the M0/M1/M3 tests). The M5 §14 acceptance lives in
# ./cmd/e2e/ and is invoked via `make metal-lima-m5`.
#
# NOTE: TestMetalHelloBoot (M0) drives the full jailer→firecracker→tap→netns→DNAT
# path and boots end-to-end here — vmmd stages the jail for the unprivileged uid
# (read-only images o+r, writable drive1 copied + chowned; ADR-019). This is the
# arm64 nested-KVM guest, so the EX44 stays the source of truth for the §14 M0
# gate. The M1/M3 tests additionally need real base/layer rootfs images (M2).
set -euo pipefail

REPO="${FAAS_REPO_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
cd "$REPO"
if [[ "$(id -u)" != 0 ]]; then
  echo "ERROR: metal tests require root inside a dedicated KVM test host." >&2
  exit 1
fi
mkdir -p /var/log/faas
./deploy/lima/stage-metal.sh
METAL_WORK=$(mktemp -d /tmp/faas-metal-run.XXXXXX)
trap 'rm -rf "$METAL_WORK"' EXIT
CGO_ENABLED=0 go build -trimpath -o "$METAL_WORK/vmmd" ./cmd/vmmd
export FAAS_TEST_VMMD_BINARY="$METAL_WORK/vmmd"
export FAAS_GUEST_INIT=/usr/local/bin/faas-guest-init
if [[ "${FAAS_METAL_BUILD_ACCEPTANCE:-0}" == 1 ]]; then
  export FAAS_BUILDER_BASE_PATH="${FAAS_BUILDER_BASE_PATH:-/srv/fc/base/runner-builder-$(go env GOARCH).ext4}"
fi

if [[ "${FAAS_METAL_BUILD_ACCEPTANCE:-0}" == 1 ]]; then
  ./deploy/lima/stage-kernel.sh
  kernel_version=$(sed -n 's/^fc_kernel_version: "\([^"]*\)"$/\1/p' deploy/ansible/roles/firecracker/defaults/main.yml)
  export FAAS_TEST_KERNEL="${FAAS_TEST_KERNEL:-/srv/fc/base/vmlinux-$kernel_version-arm64}"
else
  export FAAS_TEST_KERNEL="${FAAS_TEST_KERNEL:-/srv/fc/base/vmlinux-6.1.128}"
fi

# The V6 acceptance rootfs is refreshed by stage-metal.sh at
# /srv/fc/base/v6-{base,layer}.ext4. Both paths flow to TestMetal*:
#
#   - FAAS_TEST_BASE_ROOTFS / FAAS_TEST_LAYER_ROOTFS feed metalImages()
#     (manager_metal_test.go) — the existing M0/M1/M3 tests pass these to
#     their own ensureBusyboxExt4-style helpers, so they self-build. Setting
#     them here to the V6 rootfs means those tests use the same real
#     guest-init-bearing image, which is fine for M1 (50 concurrent cold
#     boots) and M3 (park/wake) — V6 is the only test that actively depends
#     on the post-restore resume hook.
#
#   - FAAS_TEST_V6_BASE / FAAS_TEST_V6_LAYER feed ensureV6Ext4
#     (v6_resume_ext4_metal_test.go). The helper honours these env vars and
#     skips its in-test build path when both are set — saving ~5 s per run.
V6_BASE="${FAAS_TEST_V6_BASE:-/srv/fc/base/v6-base.ext4}"
V6_LAYER="${FAAS_TEST_V6_LAYER:-/srv/fc/base/v6-layer.ext4}"
if [ ! -f "$V6_BASE" ] || [ ! -f "$V6_LAYER" ]; then
  echo "WARN: V6 rootfs not staged at $V6_BASE / $V6_LAYER" >&2
  echo "      Check the FAAS_TEST_V6_* overrides; stage-metal.sh refreshes the defaults." >&2
else
  export FAAS_TEST_V6_BASE="$V6_BASE"
  export FAAS_TEST_V6_LAYER="$V6_LAYER"
  export FAAS_TEST_BASE_ROOTFS="${FAAS_TEST_BASE_ROOTFS:-$V6_BASE}"
  export FAAS_TEST_LAYER_ROOTFS="${FAAS_TEST_LAYER_ROOTFS:-$V6_LAYER}"
fi

export FAAS_TEST_FC_VERSION="${FAAS_TEST_FC_VERSION:-$(firecracker --version | head -1 | awk '{print $2}')}"

if [ ! -e /dev/kvm ]; then
  echo "ERROR: /dev/kvm missing — nested virtualization not available." >&2
  exit 1
fi

# Root-namespace tenant bridge the per-instance veth host-side enslaves to
# (pkg/netns/config.go: TenantBridge=br-tenants, HostBridgeCIDR=10.100.0.1/16).
# The EX44 bootstrap is expected to provide this; create it here idempotently so
# the metal netns path works in the dev VM. Not persisted across guest reboots.
if ! ip link show br-tenants >/dev/null 2>&1; then
  ip link add br-tenants type bridge
  ip addr add 10.100.0.1/16 dev br-tenants
  ip link set br-tenants up
fi
sysctl -wq net.ipv4.ip_forward=1

# Production host policy provides this second NAT hop. Lima only configures
# its own/Docker subnets; without these scoped rules the builder's DNS and
# registry traffic never leaves br-tenants. Retain per-VM egress filtering.
uplink=$(ip -4 route show default | awk 'NR==1 {print $5}')
test -n "$uplink"
iptables -t nat -C POSTROUTING -s 10.100.0.0/16 -o "$uplink" -j MASQUERADE 2>/dev/null || \
  iptables -t nat -A POSTROUTING -s 10.100.0.0/16 -o "$uplink" -j MASQUERADE
iptables -C FORWARD -i br-tenants -o "$uplink" -s 10.100.0.0/16 -j ACCEPT 2>/dev/null || \
  iptables -I FORWARD 1 -i br-tenants -o "$uplink" -s 10.100.0.0/16 -j ACCEPT
iptables -C FORWARD -i "$uplink" -o br-tenants -d 10.100.0.0/16 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT 2>/dev/null || \
  iptables -I FORWARD 1 -i "$uplink" -o br-tenants -d 10.100.0.0/16 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT

# Lima-cgroup shim: the nested-KVM arm64 guest leaves /sys/fs/cgroup/faas-tenant.slice
# in a state where writing PIDs returns EBUSY (the kernel can't migrate
# processes across controllers when the slice's subtree_control is
# misconfigured vs. root). Re-mount a fresh cgroup2 ON TOP of the broken
# path so the v1.7 jailer — which always uses /sys/fs/cgroup as its v2
# unified root per /proc/mounts — lands in a writable hierarchy. The
# EX44 uses real systemd cgroup management and doesn't need this shim.
if ! mountpoint -q /sys/fs/cgroup/faas-tenant.slice; then
  if rmdir /sys/fs/cgroup/faas-tenant.slice 2>/dev/null; then
    mkdir /sys/fs/cgroup/faas-tenant.slice
    mount -t cgroup2 none /sys/fs/cgroup/faas-tenant.slice
    # Controller names are space-separated words, not one per line.
    # shellcheck disable=SC2013
    for _ctl in $(cat /sys/fs/cgroup/cgroup.controllers); do
      echo "+$_ctl" > /sys/fs/cgroup/faas-tenant.slice/cgroup.subtree_control 2>/dev/null || true
    done
  fi
fi
# Production code targets /sys/fs/cgroup directly (pkg/fcvm/cgroup.go:14
# hardcodes it; no env knob is read at runtime). The Lima cgroup shim
# above keeps that path writable; no further override is needed.

# ARM64-Lima shim: jailer reads CPU cache sizes from sysfs
# (/sys/devices/system/cpu/cpuN/cache/indexM/{size,coherency_line_size,...}) and
# panics if absent, but the arm64 nested-KVM guest doesn't expose them (firecracker
# only warns). Overmount each cache index dir with a tmpfs carrying fabricated but
# plausible values so the jailer path runs. x86_64 hosts (the EX44) expose these
# natively — this shim is a dev-loop-only concession, never needed on the box.
for _idx in /sys/devices/system/cpu/cpu[0-9]*/cache/index[0-9]*; do
  [ -d "$_idx" ] || continue
  [ -f "$_idx/size" ] && continue
  _tmp=$(mktemp -d)
  for _f in "$_idx"/*; do [ -f "$_f" ] && cat "$_f" >"$_tmp/$(basename "$_f")" 2>/dev/null || true; done
  [ -f "$_tmp/size" ] || echo "32K" >"$_tmp/size"
  [ -f "$_tmp/coherency_line_size" ] || echo "64" >"$_tmp/coherency_line_size"
  [ -f "$_tmp/number_of_sets" ] || echo "64" >"$_tmp/number_of_sets"
  [ -f "$_tmp/ways_of_associativity" ] || echo "8" >"$_tmp/ways_of_associativity"
  chmod -R a+rX "$_tmp"
  mount --bind "$_tmp" "$_idx"
done

# PR-6 / PR-6.5 (issue #911 / ADR-110): exercise `gregalectl release
# bundle|install` on the metal harness so the lockstep test
# (pkg/daemonunitspec) and the compute_nodes UPSERT
# (pkg/releaseinstall.Store) are wired end-to-end. gregalectl is the
# operator-only CLI (PR-6.5 split); gregale is the customer-facing
# CLI and does not carry release install — operators run this wire.
# Warn-and-continue: PR-6's metal wire is smoke, not gate. The real gate
# is the lockstep unit test (CI pure-Go shard) and the cmd/e2e round-trip
# test. A failure here logs but does not abort the harness so the M0/M1/
# M3/V6 §14 acceptance paths still run.
#
# FAAS_PG_DSN matches the in-guest Postgres provisioned by
# deploy/lima/faas-metal.yaml:130-138 (local trust, port 5432). Without
# the DSN, cmdReleaseInstall's best-effort UPSERT degrades to exit 3 +
# compute_node_error in the JSON report; that path is exercised by the
# cmd/e2e test which also runs with FAAS_PG_DSN unset when no DSN is
# exported locally.
export FAAS_PG_DSN="${FAAS_PG_DSN:-postgres://faas@127.0.0.1:5432/faas?sslmode=disable}"

if [[ "${RUN_GREGALE_RELEASE_INSTALL:-1}" == "1" ]] && command -v gregalectl >/dev/null 2>&1; then
    BIN_DIR="${METAL_BIN_DIR:-/tmp/faas-metal-bin}"
    if [[ -d "${BIN_DIR}" ]]; then
        GIT_SHA="$(git rev-parse HEAD 2>/dev/null || printf '0123456789abcdef0123456789abcdef01234567')"
        MANIFEST_HASH="sha256:$(printf '%064d' 0)"
        if ! gregalectl release bundle \
            --bin-dir="${BIN_DIR}" \
            --git-sha="${GIT_SHA}" \
            --manifest-hash="${MANIFEST_HASH}" \
            --releases-root=/opt/faas/releases \
        ; then
            echo "WARN: gregalectl release bundle failed (PR-6 smoke — non-fatal)" >&2
        fi
        if ! gregalectl release install \
            --git-sha="${GIT_SHA}" \
            --releases-root=/opt/faas/releases \
            --node="$(hostname)" \
        ; then
            echo "WARN: gregalectl release install failed (PR-6 smoke — non-fatal)" >&2
        fi
    else
        echo "WARN: PR-6 metal wire skipped: bin-dir ${BIN_DIR} not found" >&2
    fi
fi

RUN_TARGET="${RUN_TARGET:-./pkg/fcvm/}"
RUN_ARGS=("-run" "TestMetalHelloBoot")
if [ "$#" -gt 0 ]; then
  RUN_ARGS=("$@")
fi

echo "kernel=$FAAS_TEST_KERNEL fc=$FAAS_TEST_FC_VERSION target=$RUN_TARGET"
# -timeout 60m covers harness boot + prior M0/M1/M3/V6 metal tests + the
# §14 V2 wake-latency 100-cycle loop (cmd/e2e/deploy_wake_metal_test.go).
# Default Go test timeout is 10m which the 100-cycle loop overruns on Lima
# nested-virt even when cold-boot is cached.
status=0
go test -tags metal -count=1 -v -timeout 60m "${RUN_ARGS[@]}" "$RUN_TARGET" || status=$?
# Run even after a failing test: resource leaks must be visible in its report.
bash deploy/scripts/leakcheck.sh || status=1
exit "$status"
