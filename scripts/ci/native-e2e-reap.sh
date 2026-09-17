#!/usr/bin/env bash
# native-e2e-reap.sh — jail-chroot reaping shared by the runner and its
# contract test. Sourced, never executed.

# reap_stale_jails removes every jail chroot under the jail root — build VMs
# and app instances alike. This is a DEDICATED acceptance node: nothing but
# the gate boots microVMs here, so every chroot is the gate's own garbage.
#
# It used to remove only build-* chroots. App-instance chroots left by a
# previous run — two of them, from instances destroyed mid-wake in smoke run
# 35201413376 — then sat in /srv/fc/jail across a node STOP and START (the
# path is not tmpfs on this host; see native-acceptance-host.yml), and the
# next run's pre-flight leakcheck refused to start:
#
#   LEAK: jail chroot /srv/fc/jail/firecracker-v1.7.0-x86_64/645b161d-…/
#   native e2e: no test executed
#
# That run's own verdict already recorded those leaks; a later run must not
# be blocked by them. Kill/unmount happens before this in reap_test_microvms.
reap_stale_jails() {
  local root="${1:?jail root required}" d
  for d in "${root}"/firecracker-v*/*/; do
    [[ -d "${d}" ]] || continue
    rm -rf "${d}" 2>/dev/null || true
  done
}


# reap_test_microvms destroys microVMs a run left behind, plus the host state
# that goes with them (netns, mounts, jail chroots, build cgroups, veths).
# Called from the runner's cleanup (under set +e) AND from its pre-flight
# (under set -e), so every command that legitimately returns non-zero when
# there is nothing to do carries `|| true`. Without that, smoke run
# 35209828280 died silently at the pre-flight: pkill returned 1 because no
# firecracker was running, errexit fired, and the run reported "no test
# executed" beside a clean leakcheck.
# reap_test_microvms destroys microVMs this run left behind, plus the host
# resources that outlive them.
#
# A builder VM that wedges rides out its timeout, and the test that owns it
# gives up and moves on — but nothing destroys the VM. It then survives the
# whole run, and the NEXT run's pre-flight refuses the node outright:
#
#   native e2e: Firecracker workloads are active; drain the designated
#   acceptance node before retrying
#
# Observed on 2026-09-15: two builder VMs orphaned by run 34964279616 (one had
# been alive 15m52s against a 10-minute budget) made every phase of the next
# run fail in three seconds. The pre-flight is right to refuse — a dirty node
# makes leakcheck meaningless — so the fix is to not leave it dirty.
#
# Scoped to build-* instances: those belong to this suite. A VM the operator
# is running for another reason is not ours to kill, and on a DEDICATED
# acceptance host there should be none anyway.
reap_test_microvms() {
  local reaped=0 ns m c d

  while IFS= read -r pid; do
    [[ -n "${pid}" ]] || continue
    kill -TERM "${pid}" 2>/dev/null && reaped=$((reaped + 1)) || true
  done < <(pgrep -f 'firecracker-v[0-9]' 2>/dev/null)
  [[ "${reaped}" -eq 0 ]] || sleep 3
  pkill -KILL -f 'firecracker-v[0-9]' 2>/dev/null || true

  while IFS= read -r ns; do
    [[ -n "${ns}" ]] || continue
    ip netns delete "${ns}" 2>/dev/null || true
  done < <(ip netns list 2>/dev/null | awk '/^fc-/{print $1}')

  # Lazy umount, deepest first: a jail chroot cannot be removed while its
  # bind mounts are live, and they nest.
  while IFS= read -r m; do
    [[ -n "${m}" ]] || continue
    umount -l "${m}" 2>/dev/null || true
  done < <(awk '/firecracker-v[0-9]/{print $2}' /proc/mounts | sort -r)

  reap_stale_jails "${FAAS_E2E_JAIL_ROOT:-/srv/fc/jail}"

  for c in /sys/fs/cgroup/faas.slice/faas-cp.slice/faas-cp-build.slice/build-*; do
    [[ -d "${c}" ]] && rmdir "${c}" 2>/dev/null || true
  done

  while IFS= read -r d; do
    [[ -n "${d}" ]] || continue
    ip link delete "${d}" 2>/dev/null || true
  done < <(ip -brief link show 2>/dev/null | awk '/^vh[0-9]/{print $1}' | cut -d@ -f1)

  [[ "${reaped}" -eq 0 ]] ||
    echo "native e2e: reaped ${reaped} microVM(s) this run left running"
}
