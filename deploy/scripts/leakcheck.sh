#!/usr/bin/env bash
# leakcheck.sh — assert the box has zero leaked VM resources after tests
# (spec §Commands `make leakcheck`, invariant §6.2-4/5).
#
# Checks: no leftover fc-* network namespaces, no orphan tap devices, no jailer
# chroots under /srv/fc/jail, no lingering faas-tenant per-instance cgroup scopes.
# (jailer v1.7 rejects '.' in --id, so the actual scope name is the bare
# instance id with no 'vm-' prefix and no '.scope' suffix — see
# pkg/fcvm/cgroup.go and pkg/fcvm/config.go::PerInstanceScope.)
# Exits non-zero listing anything that leaked. Safe to run on any Linux host;
# on non-Linux (dev macs) it no-ops with a notice.
set -euo pipefail

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "leakcheck: not Linux — skipping (run on the EX44 / metal CI)"
  exit 0
fi

fail=0
note() { echo "LEAK: $*"; fail=1; }

# 1. Network namespaces
if command -v ip >/dev/null 2>&1; then
  while read -r ns; do
    [[ "$ns" == fc-* ]] && note "netns $ns"
  done < <(ip netns list 2>/dev/null | awk '{print $1}')

  # 2. Orphan tap devices in the root namespace
  while read -r dev; do
    [[ "$dev" == tap-* || "$dev" == ve-* || "$dev" =~ ^vh[0-9]+(@.*)?$ ]] && note "netdev $dev"
  done < <(ip -o link show 2>/dev/null | awk -F': ' '{print $2}')
fi

# 3. Versioned jailer instance directories (empty version parents are normal).
shopt -s nullglob
for d in /srv/fc/jail/firecracker*/*/; do
  note "jail chroot $d"
done

# 4. Current plan/builder scopes and the legacy tenant hierarchy.
for scope in /sys/fs/cgroup/faas.slice/faas-tenant.slice/tenant-*/*/ \
             /sys/fs/cgroup/faas.slice/faas-cp.slice/faas-cp-build.slice/*/ \
             /sys/fs/cgroup/faas-tenant.slice/*/; do
  case "$scope" in
    /sys/fs/cgroup/faas-tenant.slice/tenant-free/|/sys/fs/cgroup/faas-tenant.slice/tenant-hobby/|/sys/fs/cgroup/faas-tenant.slice/tenant-pro/|/sys/fs/cgroup/faas-tenant.slice/tenant-scale/) continue ;;
  esac
  [[ -e "$scope/cgroup.procs" ]] && note "cgroup scope $scope"
done

# 5. Live Firecracker processes, even if their jail was accidentally unlinked.
for exe in /proc/[0-9]*/exe; do
  target=$(readlink "$exe" 2>/dev/null || true)
  [[ "${target##*/}" == firecracker* ]] && note "process ${exe%/exe}: $target"
done

# 6. Per-instance jail and export mounts. The shared jail tmpfs is expected.
while read -r _ _ _ _ mountpoint _; do
  case "$mountpoint" in
    /srv/fc/jail/firecracker*/*/*|/tmp/faas-vmm-*|/tmp/faas-build-*) note "mount $mountpoint" ;;
  esac
done < /proc/self/mountinfo

if [[ "$fail" -ne 0 ]]; then
  echo "leakcheck FAILED"
  exit 1
fi
echo "leakcheck OK — no leaked netns/taps/jails/cgroups/processes/mounts"
