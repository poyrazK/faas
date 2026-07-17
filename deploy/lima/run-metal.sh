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
# NOTE: only TestMetalHelloBoot (M0) is expected to pass here today — the M1/M3
# tests need real base/layer rootfs images (runner-node22 etc.) that are an
# M2 deliverable and not yet staged. See deploy/lima/README.md.
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

RUN_ARGS=("-run" "TestMetalHelloBoot")
if [ "$#" -gt 0 ]; then
  RUN_ARGS=("$@")
fi

echo "kernel=$FAAS_TEST_KERNEL fc=$FAAS_TEST_FC_VERSION"
exec go test -tags metal -count=1 -v "${RUN_ARGS[@]}" ./pkg/fcvm/
