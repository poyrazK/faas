#!/usr/bin/env bash
# Run the sandbox web-mode, egress-policy and browser metal tests (pkg/fcvm
# TestMetalSandboxWebEgressPolicy, TestMetalSandboxBrowserReadsAGrantedPage)
# on a shared native KVM host, as root:
#
#   DIGEST=sha256:<amd64 manifest of ghcr.io/poyrazk/agent-base> run_sandbox_web_metal.sh
#
# beside bin/ (fcvm.metal.test, vmmd, guest-init), memory/oci_pull.py and
# leakcheck/ (deploy/scripts/leakcheck.sh and its helper). The agent base is
# the published image with this checkout's guest-init; the kernel is the
# host's Firecracker 1.7.0 guest kernel. The run holds the acceptance lock,
# refuses to start beside other Firecracker VMs, and runs leakcheck before
# and after.
set -uo pipefail
: "${DIGEST:?set DIGEST to the agent-base amd64 manifest digest}"
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
dir=$(cd "$(dirname "$0")" && pwd); bin=$dir/bin work=$dir/work
exec 9>/var/lock/faas-builder-acceptance.lock
echo "waiting for lock $(date -u +%T)"
flock -w 5400 9 || { echo "DONE lock timeout"; exit 75; }
echo "lock taken $(date -u +%T)"
for exe in /proc/[0-9]*/exe; do
  target=$(readlink "$exe" 2>/dev/null || true)
  if [[ ${target##*/} == firecracker* ]]; then echo "DONE refused: other Firecracker VMs running"; exit 75; fi
done
bash "$dir/leakcheck/leakcheck.sh" || { echo "DONE leakcheck failed before the run"; exit 1; }
mkdir -p "$work"
root=$work/rootfs
echo "== agent base from ghcr.io/poyrazk/agent-base@$DIGEST"
python3 -I "$dir/memory/oci_pull.py" ghcr.io poyrazk/agent-base "$DIGEST" "$root" || { echo "DONE pull failed"; exit 1; }
rm -f "$root/sbin/init" "$root/etc/resolv.conf"
install -m 0755 "$bin/guest-init" "$root/sbin/init"
for d in dev overlay proc run sys sys/fs/cgroup tmp etc/faas input work/output; do mkdir -p "$root/$d"; done
base_mb=$(( $(du -sm "$root" | cut -f1) * 5 / 4 + 256 ))
truncate -s "${base_mb}M" "$work/agent-base.ext4"
mkfs.ext4 -q -O '^has_journal' -d "$root" -L agent-base -F "$work/agent-base.ext4"
rm -rf --one-file-system "$root"; chmod 0644 "$work/agent-base.ext4"
echo "== metal test $(date -u +%T)"
rc=0
env TMPDIR="$work" FAAS_TEST_KERNEL=/srv/fc/base/vmlinux-6.1.134 FAAS_TEST_FC_VERSION=1.7.0 \
  FAAS_TEST_AGENT_RUN_BASE="$work/agent-base.ext4" FAAS_TEST_VMMD_BINARY="$bin/vmmd" \
  "$bin/fcvm.metal.test" -test.count=1 -test.run "${RUN:-^TestMetalSandbox(WebEgressPolicy|BrowserReadsAGrantedPage)\$}" -test.v -test.timeout 20m > "$dir/test.log" 2>&1 || rc=$?
bash "$dir/leakcheck/leakcheck.sh" >> "$dir/test.log" 2>&1 || { echo "leakcheck failed after the run"; rc=1; }
rm -rf --one-file-system "$work"
echo "DONE rc=$rc $(date -u +%T)"
exit $rc
