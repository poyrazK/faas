# ADR-983: Firecracker 1.17 and guest kernel 6.18

- Status: Proposed (pending n2 metal qualification)
- Date: 2026-10-11
- Related: ADR-005, ADR-019, ADR-022, ADR-642, ADR-680
- Supersedes: ADR-150 (Firecracker 1.7 TSC restore-order canary patch)
- Amends: spec §4.4 boot config (guest kernel line)

## Context

The fleet pins Firecracker v1.7.0 and builds guest kernel 6.1.134 from the
Firecracker v1.7.0 guest config. Upstream maintains its two newest minor
releases, currently v1.16 and v1.17 ([release
policy](https://github.com/firecracker-microvm/firecracker/blob/main/docs/RELEASE_POLICY.md)),
and supports guest 6.18 from v1.16.1 ([kernel
policy](https://github.com/firecracker-microvm/firecracker/blob/main/docs/kernel-policy.md)).
Releases since v1.7 add VMGenID, VMClock, balloon free page reporting and
huge pages on snapshot load.

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

## Qualification (n2 test node, 2026-10-11)

Firecracker 1.17.0 ran from a private PATH; the 6.18.54 kernel was built with
`scripts/build-firecracker-kernel.sh` (`sha256 3bb13ba5…`). The whole
`pkg/fcvm` metal package ran with the `run-native-metal-smoke.sh` fixtures,
once on 1.7.0 / 6.1.134 and once on 1.17.0 / 6.18.54:

| | 1.7 / 6.1 | 1.17 / 6.18 |
|---|---|---|
| Metal package (top-level tests) | 1,055 pass, 35 fail, 31 skip | 1,056 pass, 34 fail, 31 skip |
| Network-namespace batch | 7/7 | 7/7 |
| New leaks after the run | none | none |
| Restore, 30 wakes (p50 / p90) | 253 / 284 ms | 268 / 287 ms |
| Cold boot, HelloBoot ×10 (median) | 1.15 s | 1.46 s |

- No test fails only on 1.17. The 34 shared failures are host conditions:
  in-test leak checks trip on 14 chroots another session leaked on
  2026-10-08/09, plus fixtures this node lacks (public egress URL).
- A v1.7 snapshot cannot be loaded by v1.17 (`bitcode serialization error`),
  confirming the cold-boot path in decision 3.
- Firecracker 1.17 with the old 6.1 kernel cold-boots as fast as 1.7
  (median 1.16 s), and both kernels reach the network at the same time, so the
  cold-boot cost comes from the 6.18 guest after kernel init.

Follow-up fixes (2026-10-12):

1. **Guest boot CPU.** Both kernels reach the network at the same moment, but
   the 6.18 guest burned ~270 ms more CPU per boot (1,150 vs 880 ms). The
   v1.17 config builds in crypto self-tests over ~65 algorithms, SELinux and
   audit, none of which the v1.7 kernel had or Gregale uses. Cold and
   execution boots now pass `cryptomgr.notests selinux=0 audit=0`, which
   brings boot CPU to 915 ms. FIPS mode is never enabled. Interleaved
   HelloBoot medians on the n2 node (20 each): 1.7 / 6.1 1.27 s, 1.17 / 6.18
   with the old arguments 1.52 s, with the new arguments 1.33 s.
   Initcall timing is equal on both kernels, and memory is not a factor:
   peak usage is 80–89 MiB in a 128 MiB guest with no `memory.events`.
2. **PCI notice.** The guest must keep `CONFIG_PCI`: Firecracker 1.17
   describes its virtio-mmio devices in ACPI tables that reference PCI, and a
   no-PCI build fails to load them and cannot find its root disk. With the
   PCI transport off, `pci_arch_init` prints `PCI: Fatal: No config space
   access function found` at `KERN_ERR` on every boot, and Firecracker logs
   the guest's PCI port probes from its `fc_vcpu` threads. vmmd drops exactly
   that kernel line and all `fc_vcpu` lines from the customer log stream; the
   console file keeps them.
3. **No regressions.** The full metal package with these changes:
   1,057 pass, 35 fail (the same 35 as 1.7), batch 7/7, no new leaks.

Remaining: a ~60 ms median cold-boot cost from the 6.18 guest that grows
when the host is CPU-contended (+320 ms at load average 5 on the 2-vCPU test
node). The rest of the difference is guest kernel behaviour under CPU
contention. Turning guest CPU-vulnerability mitigations off would cut it
further but is a security decision, not part of this upgrade.

## Consequences

- Every app cold-boots once after its node upgrades.
- Jailer since v1.15 refuses to copy its binary onto an existing symlink or
  hard link in the chroot; vmmd must never pre-stage that path.
- Follow-ups unlocked: VMGenID kernel RNG reseed and `clock_realtime` to
  shrink the resume hook, transparent huge pages on snapshot load, balloon
  free page reporting, and `KVM_KVMCLOCK_CTRL` on pause (v1.10).
- Stay in the support window: upgrade at least every six months.
