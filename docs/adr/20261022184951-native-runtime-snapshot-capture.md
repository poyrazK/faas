# ADR 20261022184951 · Native runtime snapshot capture for park and warm

- **Status:** proposed
- **Date:** 2026-10-22
- **Related:** ADR-568 (native capture gate; amends "Remaining implementation
  sequence" item 1 for runtime captures only), ADR-595 ("Native snapshot
  publication remains unavailable until its original producer and publication
  session can be attested"), ADR-1746–1750 (funded compute requires the native
  owner), ADR-005 (cold boot must always work), ADR 20261016 (selective data
  repair; producer frames for repair launches)

## Context

**Strict compute budgets and snapshot scale-to-zero cannot run together on
main.** The 2026-10-22 native acceptance on the internal test node showed it
from both sides:

- vmmd registers the financial compute owner only with
  `native_process_recovery = true`. Without it, the bypass guard refuses every
  app under a strict compute budget and `FinancialComputeReady` never becomes
  true (`cmd/vmmd/financial_compute.go`). The wake bench's strict passes
  stopped there (run `eaedw-20261022T173244`).
- With native process recovery, `JailerVMM.SnapshotKeepAlive` refuses before any
  effect: "native recovery: snapshot publication producer is unavailable"
  (`pkg/fcvm/vmm.go`, added by #4139 under ADR-568). Every park, including the
  prime park after a deploy, fails. `TestFinancialBudgetPreviewStopMetal` and
  `TestFinancialSystemDaemonRecoveryMetal` reached a booted VM and then failed
  there (runs `eaedb-20261022T155610` and `eaedb-20261022T162411`). The native
  financial harness enables native recovery on purpose
  (`FAAS_FINANCIAL_SYSTEM_ACCEPTANCE=1`).

**Why the refusal exists.** The ordinary capture writes Firecracker's memory and
device-state files into the jail's tmpfs by pathname, widens the VM's cgroup by
pathname, freezes the private drive by pathname, and deletes a failed capture.
None of that is owned by the native journal. If vmmd dies mid-capture, recovery
cannot tell which outputs and which VM state belong to which attempt, so it can
neither finish nor safely remove them. `freezeSnapshotDrive` refuses for the
same reason ("snapshot drive export has no durable producer authority").

**What already exists.** ADR-568 built a producer-owned capture sequence for
environment qualification (`captureEnvironmentQualificationSnapshot`,
`pkg/fcvm/native_snapshot_capture_linux.go`):

- durable publication intent before any output or Firecracker effect;
- disk-backed outputs on the private persistent staging root, not tmpfs;
- the original VM memory allowance instead of pathname cgroup widening;
- one-shot pause, create and resume through the pinned control channel;
- the private drive frozen while paused;
- exclusive publication of the four-object cohort (memory, device state,
  drive, backing sidecar) with object receipts. GCS writes are
  `DoesNotExist`-conditioned; the local writer copies into `O_TMPFILE` and
  publishes with exclusive `linkat`;
- no replay of an uncertain effect.

It is reachable only with a qualification capture permit
(`nativeSnapshotCapturePermit`), which is built from the qualification incoming
and capture journal records. Ordinary launches have neither record.

Runtime captures already use a fresh namespace per capture: park and warm keys
come from `state.SnapshotCaptureMemKey(deployment, tier, uuid)` (`pkg/sched/
engine.go`). Exclusive publication's fresh-namespace rule therefore fits them
without scheduler changes.

## Decision

1. **Scope.** Ordinary (protocol-1) park and warm captures on a vmmd running
   with `native_process_recovery`. Protocol-2 admitted runtime capture
   (ADR-595 `CaptureAdmittedRuntime`), migration capture and qualification
   capture keep their current rules.

2. **Runtime capture record.** Before any hook, pause or output, vmmd writes a
   runtime capture record to the native journal. It binds the live physical
   launch record (generation, kernel boot, complete lease), a fresh capture
   UUID, the scheduler's coupled keys (memory, device state, drive, backing
   sidecar derived from the memory key) and the request deadline. The capture
   permit becomes {physical launch record, capture record, optional
   qualification incoming record}. The qualification path keeps its incoming
   record and checks unchanged.

3. **Sequence.** Under native recovery, `SnapshotKeepAlive` runs the ADR-568
   producer sequence with that permit instead of refusing. The guest hooks
   (pre-snapshot extension, the app's `before_checkpoint`) run after the record
   is written and before the pause. They are guest-channel effects and create
   no host outputs. A warm capture ends after the one-shot resume and
   publication. A park (`Snapshot`) is the same capture followed by the
   existing native stop of the VM.

4. **Completion.** A capture succeeds only after all four object receipts, the
   acknowledged resume and the final owner check. vmmd then marks the record
   complete, retires its staged outputs and publication intent, and returns an
   ordinary `SnapshotInfo`. schedd and imaged record the snapshot row exactly
   as today. Stored bytes exclude the backing sidecar, as for qualification.

5. **Failure.** An uncertain effect is never replayed.
   - Failure before the pause retires the record with no VM effect.
   - Failure after the pause does not resume the VM. The VM is retired through
     the native stop path, the scheduler records a failed capture, and the
     app's next wake cold boots (ADR-005).
   - Partial objects on GCS are deleted with the generation-conditional
     deletes of ADR-568's artifact retirement, using the same durable
     `.retiring.json` marker and periodic retry. The authority is the runtime
     capture record's own failure, not a smoke receipt, because a runtime
     snapshot is a cache entry.
   - Partial objects on the local backend are retained and counted by the
     disk-drift sweep (`snapshot_disk_drift_total`) as unexpected entries. The
     local backend cannot delete by inode identity (ADR-568), and they are
     never deleted by pathname.

6. **Recovery.** On restart, vmmd never promotes a started but incomplete
   runtime capture record. Its partial objects are handled as in 5. Its VM is
   retired by the owner path. Until ordinary launches carry producer frames
   (the follow-up below), a VM from an earlier vmmd process stays quarantined,
   as every earlier-generation native VM does today.

7. **Restore.** Unchanged. Ordinary `Restore` already stages through the
   native launch and disk-staging owners. Acceptance must prove a wake restores
   a natively captured snapshot.

8. **Gate.** The capture opens behind a new vmmd setting,
   `native_runtime_capture` (default false, accepted only with
   `native_process_recovery = true`). It stays off in the production templates
   until the acceptance below passes on a native x86_64 KVM host. Protocol-2
   captures and any vmmd whose preflight lacks the complete native adapters keep
   the current refusal. ADR-568's item 1 ("keep native production capture and
   restore closed") is amended for this scope only.

## Acceptance

On a native x86_64 KVM host, with `native_process_recovery` and
`native_runtime_capture` on:

- New `pkg/fcvm` metal tests: warm and park capture; a wake restored from the
  capture; vmmd killed before the pause, after the pause, during publication and
  after the resume. Each must show no replay, the VM retired, partial objects
  deleted (GCS) or inventoried (local), and a clean leakcheck.
- `TestFinancialBudgetPreviewStopMetal` and
  `TestFinancialSystemDaemonRecoveryMetal` pass in the financial phase of
  `run-native-e2e.sh`.
- The wake bench's baseline and strict passes both run with native recovery on,
  so their p95 values compare like for like. Native outputs go to persistent
  staging instead of tmpfs, so the pause and capture cost must be reported.
- `make test-metal` and `make leakcheck` show no new failures against main.

## Consequences

- Once the setting is on, an app under a strict compute budget can park and
  wake from snapshots on a native host.
- Native capture writes to the persistent staging disk, so each concurrent
  capture needs staging space for its memory size, and capture on HDD hosts will
  be slower than tmpfs. Admission must account for staging capacity before the
  setting is on by default.
- Failed captures on the local backend leave immutable partial objects until a
  local identity-conditional delete exists. They show up in
  `snapshot_disk_drift_total`.
- Follow-up, separate ADR: producer frames for ordinary launches (as ADR
  20261016 added for data repair), so a restarted vmmd can retire VMs launched
  by its predecessor. Native owner-restart recovery, including #4885's financial
  recovery tests, depends on it.

## Implementation notes (2026-10-22)

- The runtime authority and capture record live in the native journal under
  `runtime-captures/<instance>.json`, with backing evidence under
  `runtime-captures/backings/<capture>.json`. The capture ID is the
  scheduler's capture UUID inside the v2 memory key.
- The capture permit carries either the qualification incoming record or the
  runtime authority. Every producer check uses the same accessors and re-reads
  whichever durable record is set.
- Runtime publication intents are version 2. They carry `runtime` instead of
  `incoming`, so qualification restore and artifact retirement reject them.
- After completion vmmd removes the intent, its four receipts and the record.
  The objects then belong to the snapshot row and imaged's GC.
- The Manager no longer resumes a VM after a failed native warm capture, and
  skips its own backing-sidecar write, because the producer published that
  object exclusively.
- Not yet implemented: deleting partial objects from a failed capture. They are
  retained and logged on every backend, and the incomplete record and intent
  stay in the journals. GCS generation-conditional deletion (decision 5) must
  land before `native_runtime_capture` is enabled on a GCS-backed node.

## Rejected alternatives

- **Remove the refusal and keep the ordinary capture under native recovery.**
  Its tmpfs outputs, pathname cgroup widening and failed-capture deletion are
  exactly what native crash recovery cannot attribute (ADR-568, ADR-595).
- **Register the financial compute owner without native recovery.** After a
  vmmd crash its VMs could keep running without funded-compute retirement,
  which ADR-1746–1750 exist to prevent.
- **Cold boot every wake of a strict-budget app.** Cold boot is the fallback
  (ADR-005), not the serving path; it misses the wake latency target and costs
  more per wake.
