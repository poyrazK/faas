#!/usr/bin/env bash
# Run the agent run metal test (pkg/fcvm TestMetalAgentRun, ADR-20261027090010 step 5)
# and guest-init's agent mode tests on a shared native KVM host. Run as root.
#
#   run_agent_run_metal.sh DIR
#
# DIR holds bin/ (vmmd, vmmd-jail-helper, fcvm.metal.test, guestinit.test,
# guest-init), memory/ (scripts/experiments/agent-runs/memory, for the base
# build) and leakcheck/ (deploy/scripts/leakcheck.sh and its helper). The
# agent base is built from node:22-alpine with the three harnesses and this
# checkout's guest-init. The run holds the host acceptance lock, refuses to
# start while another session's Firecracker VMs run, runs leakcheck before
# and after, and removes its work directory.
set -euo pipefail
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

dir=$1
bin=$dir/bin work=$dir/work
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

echo "== guest-init agent mode tests"
(cd "$dir" && "$bin/guestinit.test" -test.count=1 -test.run 'Agent|OOMKill|DecideMode' -test.v 2>&1 | grep -E '^(=== RUN|--- |PASS|FAIL|ok)' )

echo "== agent base"
mkdir -p "$work"
bash "$dir/memory/build_agent_images.sh" "$work" "$bin/guest-init" "$dir/memory" pull install
rm -f "$work/rootfs/etc/resolv.conf"
base_mb=$(( $(du -sm --exclude=proc --exclude=dev "$work/rootfs" | cut -f1) * 5 / 4 + 256 ))
truncate -s "${base_mb}M" "$work/agent-base.ext4"
mkfs.ext4 -q -O '^has_journal' -d "$work/rootfs" -L agent-base -F "$work/agent-base.ext4"
rm -rf --one-file-system "$work/rootfs"
chmod 0644 "$work/agent-base.ext4"

echo "== metal test"
rc=0
env TMPDIR="$work" FAAS_TEST_KERNEL=/srv/fc/base/vmlinux-6.1.134 FAAS_TEST_FC_VERSION=1.7.0 \
  FAAS_TEST_AGENT_RUN_BASE="$work/agent-base.ext4" FAAS_TEST_VMMD_BINARY="$bin/vmmd" \
  "$bin/fcvm.metal.test" -test.count=1 -test.run '^TestMetalAgentRun$' -test.v -test.timeout 30m || rc=$?
bash "$dir/leakcheck/leakcheck.sh" || rc=1
exit "$rc"
