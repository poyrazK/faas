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

