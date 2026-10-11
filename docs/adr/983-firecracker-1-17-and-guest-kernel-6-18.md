# ADR-983: Firecracker 1.17 and guest kernel 6.18

- Status: Proposed (pending n2 metal qualification)
- Date: 2026-10-11
- Related: ADR-005, ADR-019, ADR-022, ADR-642, ADR-680
- Supersedes: ADR-150 (Firecracker 1.7 TSC restore-order canary patch)
- Amends: spec §4.4 boot config (guest kernel line)

## Context

The fleet pins Firecracker v1.7.0 and builds guest kernel 6.1.134 from the
Firecracker v1.7.0 guest config. Firecracker supports a minor release for at
least six months, and v1.7 left support on 2024-09-18 ([release
policy](https://github.com/firecracker-microvm/firecracker/blob/main/docs/RELEASE_POLICY.md)).
The current supported releases are v1.16 and v1.17. Guest kernel 6.1 reached
its minimum end of support on 2026-09-02; guest 6.18 is supported from
v1.16.1 ([kernel policy](https://github.com/firecracker-microvm/firecracker/blob/main/docs/kernel-policy.md)).

Gregale uses a narrow slice of Firecracker: config-file boot, `PATCH /vm`,
Full snapshots with the File memory backend, vsock, virtio-rng and jailer
with cgroup v2. It uses none of the fields deprecated since v1.7
(`enable_diff_snapshots`, `page_size_kib`), no custom seccomp filter and no
Firecracker metrics, so no API change is needed.

## Decision

1. Pin Firecracker v1.17.0 and guest kernel 6.18.54, built by
   `scripts/build-firecracker-kernel.sh` from kernel.org sources and the
   v1.17.0 `microvm-kernel-ci-x86_64-6.18.config`, with the existing local
   additions (VIRTIO_BLK, FUSE_FS, IP_PNP). All three inputs are pinned by
   SHA-256 in `roles/firecracker/defaults/main.yml`.
2. Move the binary and the kernel together. The v1.17 configs boot through
   ACPI with MPTable parsing off, so the new kernel cannot boot under v1.7
   and the old one is outside v1.17's support window. Kernels stay keyed by
   Firecracker version (`kernel/<version>`), so a node running either release
   finds its own kernel.
3. Snapshots are not migrated. The snapshot format changed in v1.11, v1.12
   and v1.15, and every snapshot records `fc_version`; a mismatch takes the
   existing cold-boot path (`fc_version_mismatch`, ADR-005) and the app
   re-snapshots. Migration carries the snapshot's version to the destination,
   so a cross-version move also cold-boots instead of failing.
4. Retire the ADR-150 TSC restore-order patch. Upstream fixed the ordering in
   v1.8 (#4618, #4666). The patch files remain for the v1.7 sparse-memory
   experiment only.
5. Keep the ADR-642 guest timer pins (`clocksource=kvm-clock
   lapic=notscdeadline`) and the ADR-022/680 resume hook unchanged in this
   step. Relaxing them (VMGenID reseed, `clock_realtime` on load) needs its
   own measurement after the upgrade.

## Rollout

1. Qualify on the n2 test node with the binaries on a private PATH: metal
   boot, restore, park/wake, sidecars, builder VMs, leakcheck, the stale
   snapshot path (a v1.7 snapshot cold-boots on v1.17) and the wake benchmark
   against v1.7.
2. Release with the new kernel digest. Pause cross-node rebalancing while
   nodes run different versions; upgrade one node at a time (drain, upgrade,
   undrain). Expect one cold boot per app; prewarm apps with traffic.
3. Rollback reinstalls v1.7.0 and the 6.1 kernel. Snapshots taken on v1.17
   go stale and cold-boot; no state is lost because snapshots are cache.

## Consequences

- Every app cold-boots once after its node upgrades.
- Jailer since v1.15 refuses to copy its binary onto an existing symlink or
  hard link in the chroot; vmmd must never pre-stage that path.
- Follow-ups unlocked: VMGenID kernel RNG reseed and `clock_realtime` to
  shrink the resume hook, transparent huge pages on snapshot load, balloon
  free page reporting, and `KVM_KVMCLOCK_CTRL` on pause (v1.10).
- Stay in the support window: upgrade at least every six months.
