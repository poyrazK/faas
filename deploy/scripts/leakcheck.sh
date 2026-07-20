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
    [[ "$dev" == tap-* || "$dev" == ve-* ]] && note "netdev $dev"
  done < <(ip -o link show 2>/dev/null | awk -F': ' '{print $2}')
fi

# 3. Jailer chroots
if [[ -d /srv/fc/jail ]]; then
  shopt -s nullglob
  for d in /srv/fc/jail/*/; do
    note "jail chroot $d"
  done
fi

# 4. Tenant VM cgroup scopes
# The scope directory name == instance id (no 'vm-' prefix, no '.scope'
# suffix — jailer v1.7 rejects '.' in --id). Treat any child dir that
# looks like a per-VM scope (has cgroup.procs or memory.max) as a leak.
if [[ -d /sys/fs/cgroup/faas-tenant.slice ]]; then
  shopt -s nullglob dotglob
  for scope in /sys/fs/cgroup/faas-tenant.slice/*/; do
    if [[ -e "$scope/cgroup.procs" || -e "$scope/memory.max" ]]; then
      note "cgroup scope $scope"
    fi
  done
fi

if [[ "$fail" -ne 0 ]]; then
  echo "leakcheck FAILED"
  exit 1
fi
echo "leakcheck OK — no leaked netns/taps/jails/cgroups"
