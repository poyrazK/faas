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
#
# NOTE: TestMetalHelloBoot (M0) drives the full jailer→firecracker→tap→netns→DNAT
# path and boots end-to-end here — vmmd stages the jail for the unprivileged uid
# (read-only images o+r, writable drive1 copied + chowned; ADR-019). This is the
# arm64 nested-KVM guest, so the EX44 stays the source of truth for the §14 M0
# gate. The M1/M3 tests additionally need real base/layer rootfs images (M2).
set -euo pipefail

export FAAS_TEST_KERNEL="${FAAS_TEST_KERNEL:-/srv/fc/base/vmlinux-6.1.128}"
# M0 (TestMetalHelloBoot) overrides base/layer with a busybox image it builds
# via mkfs.ext4; these just need to be non-empty so metalImages() doesn't Skip.
export FAAS_TEST_BASE_ROOTFS="${FAAS_TEST_BASE_ROOTFS:-$FAAS_TEST_KERNEL}"
export FAAS_TEST_LAYER_ROOTFS="${FAAS_TEST_LAYER_ROOTFS:-$FAAS_TEST_KERNEL}"
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
    for _ctl in $(cat /sys/fs/cgroup/cgroup.controllers); do
      echo "+$_ctl" > /sys/fs/cgroup/faas-tenant.slice/cgroup.subtree_control 2>/dev/null || true
    done
  fi
fi
# Re-use the original /sys/fs/cgroup path the production code already targets;
# no need to point cgroupRoot elsewhere on Lima.
export FAAS_LIMA_CGROUP_ROOT="/sys/fs/cgroup"

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

RUN_ARGS=("-run" "TestMetalHelloBoot")
if [ "$#" -gt 0 ]; then
  RUN_ARGS=("$@")
fi

echo "kernel=$FAAS_TEST_KERNEL fc=$FAAS_TEST_FC_VERSION"
exec go test -tags metal -count=1 -v "${RUN_ARGS[@]}" ./pkg/fcvm/
