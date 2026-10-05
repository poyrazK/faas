# ADR-192 · Wake hot path: single pre-boot staging session and full attribution

- **Status:** accepted
- **Date:** 2026-09-21
- **Decision:** on every restore and cold boot vmmd writes all per-instance
  files onto drive1 in one loop-mount session, and the
  `wake.restore_breakdown` event carries the Manager.Wake phases that run
  before the JailerVMM window plus a separate `stage_pre_boot_files_ms`.
- **Why:** production wake timelines (10 samples, 2026-09-20) showed the
  restore "stage_snapshot" window at a median of 88 ms and up to 355 ms under
  load, while the mem/vmstate staging it nominally measured is two syscalls.
  The cost was two to three `mount -o loop` / `umount` cycles per wake for
  `secrets.env`, `env.json` and the service resolver. Separately, the slowest
  observed wake (6.6 s platform interval) had 5.2 s before the vmmd restore
  timer started and nothing on the customer timeline could attribute it.
- **Consequences:** apps with secrets or API env pay one ext4 mount per wake
  instead of up to three; `stage_snapshot_ms` now measures only the snapshot
  bind and shrinks accordingly (dashboards comparing it across the change
  will see a step); `wake.restore_breakdown` gains
  `lease_acquire_ms`, `env_prepare_ms`, `pre_network_ms`, `setup_network_ms`
  (outside `total_ms`) and `stage_pre_boot_files_ms` (inside it). No wire,
  schema or guest contract changes; the public `Stage*` methods keep their
  one-file-per-mount behaviour for the legacy Manager path.
- **Rejected alternatives:** skipping the writes when the sealed env is
  unchanged since capture (needs a durable env hash next to the snapshot and
  an ADR on env-change-vs-snapshot semantics — a follow-up, not this change);
  delivering env through the resume-hook payload (changes the guest-init
  contract); replacing the loop mount with `debugfs` writes (unsafe against a
  live journal); making the post-readiness audit row asynchronous (saves one
  round trip but `pkg/sched/events_test.go` pins the synchronous row and the
  `boot_completed` emit is already async).

## Context

`JailerVMM.Restore` stages the chroot, then calls `stagePreBootFiles`, then
bind-mounts the mem and vmstate files and starts the jailer. Until this ADR
`stagePreBootFiles` delegated to `StageSecretsEnv`, `StageAPIEnv`,
`stageServiceDiscoveryResolver`, `StageWorkloadEnv`, `StageWorkloadManifest`
and `StageWorkloadRoster`, each of which mounted drive1 on its own loop
device, wrote one file and unmounted. Every unmount flushes the ext4 journal
of the freshly reflinked layer to the artifact disk — the same pd-ssd the
guest is about to page-fault its memory from.

The rc.98 acceptance run (quiet node, warm cache) put this window at 26 ms
sequential and 43 ms under a three-way burst. Production put it at 88 ms
median and 355 ms maximum. It was labelled `stage_snapshot_ms`, so it read as
snapshot I/O.

Manager.Wake already timed `lease_acquire`, `env_prepare`, `pre_network`,
`setup_network` and `bring_up`, but only into a slog line that fires on
failure or when the wake exceeds 20 s. The customer-facing timeline
(`gregale wake-timeline`) had no row for them.

## Operator staging attribution (2026-10-03)

The existing Debug `restore timing breakdown` record also splits the pre-boot
staging interval into payload validation/construction/digest (`pre_boot_prepare_us`),
the host mount command (`pre_boot_mount_ms`), captured-file comparison
(`pre_boot_check_us`), all file writers (`pre_boot_write_us`), and the host unmount
command (`pre_boot_unmount_ms`). The two command intervals include process start
and the complete command round trip; they do not isolate kernel execution.
`pre_boot_files_total` counts intended writers and `pre_boot_files_written` counts
successfully completed writers. The existing `stage_pre_boot_files_skipped` means
the capture digest avoided the mount; a mounted identical-file check can instead
have zero writes without setting that flag.

These diagnostics stay operator-only, like the existing TUN subphases. The
customer event, metric names, guest contract, file contents, mount options,
write order, error propagation and cleanup behavior are unchanged. No file
contents, environment values or digests are logged. The sum of these subphases
can be below `stage_pre_boot_files_ms`: drive resolution, ledger work, temporary
directory setup/cleanup and integer rounding remain in the outer interval.
Native restore acceptance and leak checks remain required for lifecycle changes.

The 2026-10-02 saved latency sample recorded 36–101 ms in this aggregate phase,
but contained no measurements distinguishing host mount/unmount from file work.
It therefore does not justify attributing the interval to either component or
claiming a measured gain from this instrumentation. In particular, the existing
whole-capture skip must still miss when a new instance changes `env.json`.
