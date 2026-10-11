#!/usr/bin/env bash
# scripts/prebuild-kernel.sh — build the Firecracker-pinned Linux kernel
# inside the image at /srv/fc/base/vmlinux-<version>.
#
# Per ADR-005: image bakes ONE kernel version, pinned via SHA-256. The
# FC kernel config from
# https://raw.githubusercontent.com/firecracker-microvm/firecracker/v<fc_release>/
#   resources/guest_configs/microvm-kernel-ci-x86_64-6.18.config
# is the source of truth for what built-in features the kernel has
# (CONFIG_BLK_DEV_VIRTIO=y is the load-bearing one).
#
# This script runs ONCE per image build. The build host has the kernel
# source mounted at /tmp/src; the build itself targets /srv/fc/base.
set -euo pipefail

FC_KERNEL_VERSION="${FC_KERNEL_VERSION:-6.18.54}"
FC_RELEASE_VERSION="${FC_RELEASE_VERSION:-1.17.0}"
FC_KERNEL_TAR_URL="${FC_KERNEL_TAR_URL:-https://cdn.kernel.org/pub/linux/kernel/v6.x/linux-${FC_KERNEL_VERSION}.tar.xz}"
FC_KERNEL_TAR_SHA256="${FC_KERNEL_TAR_SHA256:-9df30b02dd8102bbd0be52556288ef6889ddbe7f1ddb96fbf847d0becf3eacac}"
FC_KERNEL_CONFIG_URL="${FC_KERNEL_CONFIG_URL:-https://raw.githubusercontent.com/firecracker-microvm/firecracker/v1.17.0/resources/guest_configs/microvm-kernel-ci-x86_64-6.18.config}"

BASE_DIR="/srv/fc/base"
mkdir -p "${BASE_DIR}"

TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

echo "prebuild-kernel: downloading kernel source ${FC_KERNEL_VERSION}…"
curl --fail --silent --show-error --location --retry 3 --retry-delay 2 \
    -o "${TMP}/linux.tar.xz" "${FC_KERNEL_TAR_URL}"
echo "${FC_KERNEL_TAR_SHA256}  ${TMP}/linux.tar.xz" | sha256sum --check --strict
(cd "${TMP}" && tar -xJf linux.tar.xz)

KERNEL_SRC="${TMP}/linux-${FC_KERNEL_VERSION}"
echo "prebuild-kernel: downloading FC kernel config…"
curl --fail --silent --show-error --location \
    -o "${KERNEL_SRC}/.config" "${FC_KERNEL_CONFIG_URL}"

# Per the FC kernel config: CONFIG_BLK_DEV_VIRTIO must be built-in, not
# modular (kernel mounts root during boot).
sed -i 's/^CONFIG_BLK_DEV_VIRTIO=m$/CONFIG_BLK_DEV_VIRTIO=y/' "${KERNEL_SRC}/.config" || true

# Rootless BuildKit uses fuse-overlayfs for copy-on-write layers. Keep FUSE
# built into the guest kernel (rather than relying on a module that is absent
# from the minimal Firecracker rootfs), so /dev/fuse works in builder VMs.
if grep -q '^CONFIG_FUSE_FS=' "${KERNEL_SRC}/.config"; then
    sed -i 's/^CONFIG_FUSE_FS=.*/CONFIG_FUSE_FS=y/' "${KERNEL_SRC}/.config"
elif grep -q '^# CONFIG_FUSE_FS is not set$' "${KERNEL_SRC}/.config"; then
    sed -i 's/^# CONFIG_FUSE_FS is not set$/CONFIG_FUSE_FS=y/' "${KERNEL_SRC}/.config"
else
    printf '\nCONFIG_FUSE_FS=y\n' >> "${KERNEL_SRC}/.config"
fi

# guest-init supplies a static `ip=` kernel argument for the Firecracker
# point-to-point link. Enable the kernel's built-in IP autoconfiguration so
# eth0 is configured before the init process starts.
if grep -q '^CONFIG_IP_PNP=' "${KERNEL_SRC}/.config"; then
    sed -i 's/^CONFIG_IP_PNP=.*/CONFIG_IP_PNP=y/' "${KERNEL_SRC}/.config"
elif grep -q '^# CONFIG_IP_PNP is not set$' "${KERNEL_SRC}/.config"; then
    sed -i 's/^# CONFIG_IP_PNP is not set$/CONFIG_IP_PNP=y/' "${KERNEL_SRC}/.config"
else
    printf 'CONFIG_IP_PNP=y\n' >> "${KERNEL_SRC}/.config"
fi

# Build deps per firecracker docs: make gcc flex bison libssl-dev
# libelf-dev bc kmod cpio xz.
apt-get update -qq
apt-get install -y --no-install-recommends \
    build-essential flex bison libssl-dev libelf-dev bc kmod cpio xz-utils

cd "${KERNEL_SRC}"

# Build with -j$(nproc) — 4 cores on cx22 / t3.medium; ~5 min wall.
make -j"$(nproc)" bzImage

# Move vmlinux (the uncompressed kernel FC boots with) to /srv/fc/base.
# arch/x86/boot/bzImage is the compressed form; vmlinux is at the top.
install -m 0644 vmlinux "${BASE_DIR}/vmlinux-${FC_KERNEL_VERSION}"
mkdir -p /srv/fc/kernel
install -m 0644 vmlinux "/srv/fc/kernel/${FC_RELEASE_VERSION}"

# SHA-256 pin — what Release.KernelDigest asserts at doctor time.
sha256sum "${BASE_DIR}/vmlinux-${FC_KERNEL_VERSION}" \
    | awk '{print $1}' > "${BASE_DIR}/vmlinux-${FC_KERNEL_VERSION}.sha256"

echo "prebuild-kernel: $(cat ${BASE_DIR}/vmlinux-${FC_KERNEL_VERSION}.sha256)  vmlinux-${FC_KERNEL_VERSION}"
