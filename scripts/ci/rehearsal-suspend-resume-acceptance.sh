#!/usr/bin/env bash
# ADR-20261017201014: run only on a dedicated native x86 Linux KVM qualification host.
set -euo pipefail

if [[ $(uname -s) != Linux || $(uname -m) != x86_64 || $EUID != 0 || ! -c /dev/kvm ]]; then
  echo 'Suspend/resume rehearsal acceptance requires root on a dedicated native x86_64 Linux KVM host.' >&2
  exit 1
fi

: "${FAAS_TEST_KERNEL:?Set the staged kernel path}"
: "${FAAS_TEST_BASE_ROOTFS:?Set the staged base rootfs path}"
: "${FAAS_TEST_LAYER_ROOTFS:?Set the staged layer rootfs path}"
: "${FAAS_TEST_FC_VERSION:?Set the installed Firecracker version}"
: "${FAAS_TEST_V6_BASE:?Set the staged V6 base with the real guest resume hook}"
: "${FAAS_TEST_V6_LAYER:?Set the staged V6 writable layer}"
for image in "$FAAS_TEST_KERNEL" "$FAAS_TEST_BASE_ROOTFS" "$FAAS_TEST_LAYER_ROOTFS" "$FAAS_TEST_V6_BASE" "$FAAS_TEST_V6_LAYER"; do
  test -r "$image"
done
for tool in firecracker jailer busybox ip nft python3 mkfs.ext4; do
  command -v "$tool" >/dev/null
done

export FAAS_TEST_REHEARSAL_SUSPEND_RESUME=1
export RUN_REGEX='^TestMetalRehearsalSuspendResume$'
make test-metal PKGS='./pkg/fcvm' RUN_ARGS='-p=1'
make leakcheck
