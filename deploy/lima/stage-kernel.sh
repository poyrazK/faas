#!/usr/bin/env bash
# ARM64 dev counterpart of scripts/build-firecracker-kernel.sh. Use the same
# source pin and required built-ins; production still consumes its x86_64 bundle.
set -euo pipefail
REPO="${FAAS_REPO_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
cd "$REPO"
defaults=deploy/ansible/roles/firecracker/defaults/main.yml
value() { sed -n "s/^$1: \"\([^\"]*\)\"$/\1/p" "$defaults"; }
version=$(value fc_kernel_version)
sha=$(value fc_kernel_tar_sha256)
release=$(value fc_release)
config_sha=cc8227deb08729100099cb0ec946f90dff6ef712691d80afe213e0ebf453fd46
out="/srv/fc/base/vmlinux-$version-arm64"
key=$(cat "$0" "$defaults" | sha256sum | cut -d' ' -f1)
if [[ -f "$out" && "$(cat "$out.recipe" 2>/dev/null || true)" == "$key" ]]; then
  (cd /srv/fc/base && sha256sum -c "$(basename "$out").sha256")
  exit 0
fi
work=$(mktemp -d /var/tmp/faas-metal-kernel.XXXXXX)
trap 'rm -rf "$work"' EXIT
curl -fsSL --retry 3 "https://cdn.kernel.org/pub/linux/kernel/v6.x/linux-$version.tar.xz" -o "$work/linux.tar.xz"
echo "$sha  $work/linux.tar.xz" | sha256sum -c -
tar -xJf "$work/linux.tar.xz" -C "$work"
src="$work/linux-$version"
curl -fsSL --retry 3 "https://raw.githubusercontent.com/firecracker-microvm/firecracker/$release/resources/guest_configs/microvm-kernel-ci-aarch64-6.1.config" -o "$src/.config"
echo "$config_sha  $src/.config" | sha256sum -c -
"$src/scripts/config" --file "$src/.config" --enable VIRTIO_BLK --enable FUSE_FS --enable IP_PNP
export KBUILD_BUILD_TIMESTAMP='1970-01-01 00:00:00 UTC'
export KBUILD_BUILD_USER=gregale KBUILD_BUILD_HOST=lima-metal KBUILD_BUILD_VERSION=1
make -C "$src" ARCH=arm64 olddefconfig
for option in VIRTIO_BLK FUSE_FS IP_PNP OVERLAY_FS USER_NS; do
  grep -q "^CONFIG_$option=y$" "$src/.config"
done
make -C "$src" -j"$(nproc)" ARCH=arm64 Image
mkdir -p /srv/fc/base
# Aarch64 Firecracker boots the uncompressed Image, not the ELF vmlinux.
install -m0644 "$src/arch/arm64/boot/Image" "$out"
install -m0644 "$src/.config" "$out.config"
(cd /srv/fc/base && sha256sum "$(basename "$out")" > "$(basename "$out").sha256")
printf '%s\n' "$key" > "$out.recipe"
echo "Staged builder-capable guest kernel: $out"
