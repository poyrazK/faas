#!/usr/bin/env bash
# Build the release-pinned Firecracker guest kernel once in release CI.
#
# Production compute nodes consume the resulting vmlinux from the signed
# daemon bundle. They must not rebuild it locally: compiler versions,
# hostnames, and build timestamps otherwise make the kernel digest differ
# between otherwise identical providers.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
defaults_file=${FIRECRACKER_DEFAULTS_FILE:-"$repo_root/deploy/ansible/roles/firecracker/defaults/main.yml"}
output=""

usage() {
  cat >&2 <<'USAGE'
usage: build-firecracker-kernel.sh --output PATH
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --output)
      [[ $# -ge 2 ]] || { usage; exit 2; }
      output=$2
      shift 2
      ;;
    -h|--help)
      usage >&2
      exit 0
      ;;
    *)
      usage
      exit 2
      ;;
  esac
done

[[ -n "$output" ]] || { usage; exit 2; }
[[ -f "$defaults_file" ]] || {
  echo "build-firecracker-kernel: defaults file not found: $defaults_file" >&2
  exit 1
}

default_value() {
  local key=$1 value
  value=$(sed -n "s/^${key}:[[:space:]]*\"\([^\"]*\)\"[[:space:]]*$/\1/p" "$defaults_file" | head -n 1)
  [[ -n "$value" ]] || {
    echo "build-firecracker-kernel: missing quoted $key in $defaults_file" >&2
    exit 1
  }
  printf '%s' "$value"
}

fc_release=$(default_value fc_release)
kernel_version=$(default_value fc_kernel_version)
kernel_tar_url=$(default_value fc_kernel_tar_url)
kernel_tar_sha256=$(default_value fc_kernel_tar_sha256)
kernel_config_url=$(default_value fc_kernel_config_url)
kernel_config_sha256=$(default_value fc_kernel_config_sha256)

# The role defaults are the single source of truth, but Ansible normally
# renders these two URLs before using them. Render the same two placeholders
# here so release CI downloads the pinned bytes rather than the literal
# Jinja expressions from defaults/main.yml.
kernel_tar_url=${kernel_tar_url//\{\{ fc_kernel_version \}\}/$kernel_version}
kernel_config_url=${kernel_config_url//\{\{ fc_release \}\}/$fc_release}

[[ "$kernel_tar_sha256" =~ ^[0-9a-f]{64}$ ]] || {
  echo "build-firecracker-kernel: invalid kernel tarball pin" >&2
  exit 1
}
[[ "$kernel_config_sha256" =~ ^[0-9a-f]{64}$ ]] || {
  echo "build-firecracker-kernel: invalid kernel config pin" >&2
  exit 1
}
[[ "$kernel_tar_url" != *'{{ '* && "$kernel_config_url" != *'{{ '* ]] || {
  echo "build-firecracker-kernel: unresolved Jinja placeholder in kernel URL" >&2
  exit 1
}

work_root=$(mktemp -d "${TMPDIR:-/tmp}/gregale-firecracker-kernel.XXXXXX")
trap 'rm -rf "$work_root"' EXIT
source_tar="$work_root/linux-${kernel_version}.tar.xz"
source_dir="$work_root/linux-${kernel_version}"
config_file="$work_root/config-${kernel_version}"

curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 \
  -o "$source_tar" "$kernel_tar_url"
echo "$kernel_tar_sha256  $source_tar" | sha256sum -c -
tar --extract --xz --file "$source_tar" --directory "$work_root"

curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 \
  -o "$config_file" "$kernel_config_url"
echo "$kernel_config_sha256  $config_file" | sha256sum -c -
install -m 0644 "$config_file" "$source_dir/.config"

# These are the only supported local additions to Firecracker's pinned
# guest config. Keep the old, non-existent BLK_DEV_VIRTIO symbol out of the
# release build; VIRTIO_BLK is the actual 6.1 kernel option.
sed -i \
  -e 's/^# CONFIG_VIRTIO_BLK is not set$/CONFIG_VIRTIO_BLK=y/' \
  -e 's/^CONFIG_VIRTIO_BLK=.*/CONFIG_VIRTIO_BLK=y/' \
  -e 's/^# CONFIG_FUSE_FS is not set$/CONFIG_FUSE_FS=y/' \
  -e 's/^CONFIG_FUSE_FS=.*/CONFIG_FUSE_FS=y/' \
  -e 's/^# CONFIG_IP_PNP is not set$/CONFIG_IP_PNP=y/' \
  -e 's/^CONFIG_IP_PNP=.*/CONFIG_IP_PNP=y/' \
  "$source_dir/.config"
grep -q '^CONFIG_VIRTIO_BLK=y$' "$source_dir/.config"
grep -q '^CONFIG_FUSE_FS=y$' "$source_dir/.config"
grep -q '^CONFIG_IP_PNP=y$' "$source_dir/.config"

# Reproducibility is useful even though hosts consume the signed bytes. The
# fixed metadata prevents a release rebuild from silently changing the
# kernel merely because it ran at a different time or on a different runner.
export SOURCE_DATE_EPOCH=0
export KBUILD_BUILD_TIMESTAMP='1970-01-01 00:00:00 UTC'
export KBUILD_BUILD_USER=gregale
export KBUILD_BUILD_HOST=release-builder
export KBUILD_BUILD_VERSION=1

make -C "$source_dir" -j"$(nproc)" ARCH=x86_64 CROSS_COMPILE= olddefconfig
make -C "$source_dir" -j"$(nproc)" ARCH=x86_64 CROSS_COMPILE= vmlinux

file -b "$source_dir/vmlinux" | grep -q 'ELF 64-bit.*x86-64'
mkdir -p "$(dirname "$output")"
install -m 0644 "$source_dir/vmlinux" "$output"
printf 'built release kernel %s\n' "$(sha256sum "$output" | awk '{print $1}')"
