#!/usr/bin/env bash
# Run the sandbox root metal test (pkg/fcvm TestMetalSandboxRootStaysInsideTheVM,
# ADR-20261027090013 §10) on a shared native KVM host. Run as root.
#
#   run_sandbox_root_metal.sh DIR
#
# DIR holds bin/ (vmmd, fcvm.metal.test, guest-init), memory/
# (scripts/experiments/agent-runs/memory, for the base build), leakcheck/
# (deploy/scripts/leakcheck.sh and its helper) and kernel/
# (scripts/build-firecracker-kernel.sh). The guest kernel is built by the
# release script with this checkout's edits, against the host's Firecracker:
# FIRECRACKER_* below pin the host's kernel line (default: Firecracker 1.7.0
# with Linux 6.1.134, its CI config). The run holds the host acceptance lock,
# refuses to start while another session's Firecracker VMs run, runs
# leakcheck before and after, and removes its work directory.
set -euo pipefail
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

dir=$1
bin=$dir/bin work=$dir/work
fc_release=${FIRECRACKER_RELEASE:-v1.7.0}
fc_version=${FIRECRACKER_VERSION:-1.7.0}
kernel_version=${FIRECRACKER_KERNEL_VERSION:-6.1.134}
kernel_tar_sha256=${FIRECRACKER_KERNEL_TAR_SHA256:-60c70cdd70ddee384c004242e67844e3dd1fe28f75b26b3586859fff0a07bd23}
kernel_config=${FIRECRACKER_KERNEL_CONFIG:-microvm-kernel-ci-x86_64-6.1.config}
kernel_config_sha256=${FIRECRACKER_KERNEL_CONFIG_SHA256:-9278a7cdecbf3371c3dee7209b0b586323c649af885b106e5edc35f4073fe225}

exec 9>/var/lock/faas-builder-acceptance.lock
echo "run: waiting for the acceptance lock"
flock -w "${LOCK_WAIT_S:-3600}" 9 || { echo "run: acceptance lock held" >&2; exit 75; }
for exe in /proc/[0-9]*/exe; do
  target=$(readlink "$exe" 2>/dev/null || true)
  if [[ ${target##*/} == firecracker* ]]; then
    echo "run: another session's Firecracker VMs are running; stopping" >&2
    exit 75
  fi
done
# shellcheck disable=SC2329 # run by the EXIT trap
cleanup() {
  for m in "$work/rootfs/dev" "$work/rootfs/proc"; do
    if mountpoint -q "$m" 2>/dev/null; then umount "$m"; fi
  done
  if ! findmnt -rn -o TARGET | grep -q "^$work"; then
    rm -rf --one-file-system "$work"
  fi
}
trap cleanup EXIT
bash "$dir/leakcheck/leakcheck.sh"
mkdir -p "$work"

echo "== guest kernel (no modules, no kexec)"
cat > "$work/firecracker-defaults.yml" <<EOF
fc_release: "$fc_release"
fc_kernel_version: "$kernel_version"
fc_kernel_tar_url: "https://cdn.kernel.org/pub/linux/kernel/v6.x/linux-{{ fc_kernel_version }}.tar.xz"
fc_kernel_tar_sha256: "$kernel_tar_sha256"
fc_kernel_config_url: "https://raw.githubusercontent.com/firecracker-microvm/firecracker/{{ fc_release }}/resources/guest_configs/$kernel_config"
fc_kernel_config_sha256: "$kernel_config_sha256"
EOF
# The kernel is kept in DIR, beside its build script, so a rerun with the
# same script reuses it.
kernel=$dir/kernel/vmlinux
if [[ ! -s $kernel || $dir/kernel/build-firecracker-kernel.sh -nt $kernel ]]; then
  TMPDIR=$work FIRECRACKER_DEFAULTS_FILE="$work/firecracker-defaults.yml" \
    bash "$dir/kernel/build-firecracker-kernel.sh" --output "$kernel" > "$work/kernel-build.log" 2>&1 ||
    { tail -40 "$work/kernel-build.log" >&2; exit 1; }
  tail -1 "$work/kernel-build.log"
fi

echo "== agent base"
bash "$dir/memory/build_agent_images.sh" "$work" "$bin/guest-init" "$dir/memory" pull install
rm -f "$work/rootfs/etc/resolv.conf"
base_mb=$(( $(du -sm --exclude=proc --exclude=dev "$work/rootfs" | cut -f1) * 5 / 4 + 256 ))
truncate -s "${base_mb}M" "$work/agent-base.ext4"
mkfs.ext4 -q -O '^has_journal' -d "$work/rootfs" -L agent-base -F "$work/agent-base.ext4"
rm -rf --one-file-system "$work/rootfs"
chmod 0644 "$work/agent-base.ext4"

echo "== metal test"
rc=0
env TMPDIR="$work" FAAS_TEST_KERNEL="$kernel" FAAS_TEST_FC_VERSION="$fc_version" \
  FAAS_TEST_AGENT_RUN_BASE="$work/agent-base.ext4" FAAS_TEST_VMMD_BINARY="$bin/vmmd" \
  "$bin/fcvm.metal.test" -test.count=1 -test.run '^TestMetalSandboxRootStaysInsideTheVM$' -test.v -test.timeout 30m || rc=$?
bash "$dir/leakcheck/leakcheck.sh" || rc=1
exit "$rc"
