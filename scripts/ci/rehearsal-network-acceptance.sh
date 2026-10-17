#!/usr/bin/env bash
# ADR-1349: run only on the dedicated native x86_64 KVM acceptance host.
set -euo pipefail

if [[ $(uname -s) != Linux || $(uname -m) != x86_64 || $EUID != 0 || ! -c /dev/kvm ]]; then
  echo 'Rehearsal containment acceptance requires root on a dedicated native x86_64 Linux KVM host.' >&2
  exit 1
fi

: "${FAAS_TEST_REHEARSAL_PUBLIC_IP:?Set an operator-owned public IPv4 probe endpoint with TCP 80 and 443 open}"
: "${FAAS_TEST_KERNEL:?Set the staged kernel path}"
: "${FAAS_TEST_BASE_ROOTFS:?Set the staged base rootfs path}"
: "${FAAS_TEST_LAYER_ROOTFS:?Set the staged layer rootfs path}"
: "${FAAS_TEST_FC_VERSION:?Set the installed Firecracker version}"
for image in "$FAAS_TEST_KERNEL" "$FAAS_TEST_BASE_ROOTFS" "$FAAS_TEST_LAYER_ROOTFS"; do
  test -r "$image"
done
for tool in firecracker jailer busybox ip nft python3 mkfs.ext4; do
  command -v "$tool" >/dev/null
done

# The host must have its test tenant bridge prepared and the canonical proxy
# ports 53/5354/10081 free. The guest test owns mock DNS and HTTP dependencies.
export FAAS_TEST_REHEARSAL_NETWORK=1
export RUN_REGEX='^TestMetalRehearsalNetwork'
make test-metal PKGS='./pkg/netns ./pkg/fcvm' RUN_ARGS='-p=1'
make leakcheck
