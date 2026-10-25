# ADR 20261023093000 · Native qualification capture opens behind a host opt-in

- **Status:** accepted for acceptance hosts; production stays closed
- **Date:** 2026-10-23
- **Related:** ADR-568 (environment GitOps; native capture/restore gate),
  ADR-1646 (candidate binding qualification), ADR 20261022184951 (native
  runtime capture), ADR 20261021153953 (push-bound worker listener),
  ADR 20261022155812 (pull-queue smoke leases)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

ADR-568 keeps native qualification capture closed until capture and restore
pass dedicated acceptance. In code that was
`JailerVMM.checkEnvironmentQualificationSnapshotSupport`, which refused every
capture unconditionally. The rest of the path, from the original candidate
through native capture, journal-backed restore, smoke and owner-authorized
retirement, is implemented. The gate also refused the acceptance run that is
meant to prove it, so the native GitOps and managed-service journeys could
never get past capture.

On the n2 test node, every earlier prerequisite now passes: nftables 1.1.3,
loose `rp_filter` on `br-tenants`, and the push-worker listener. A graph
reaches capture and stops there with
`environment_qualification_unconfirmed`. The native capture protocol itself
(`TestMetalNativeCaptureSequence`: complete, lost_create, lost_resume)
passed on n2 under the runtime-capture work.

## Decision

- **The opt-in.** vmmd gains `native_qualification_capture`, off by default.
  `LoadConfig` refuses it unless `native_process_recovery = true` and both
  `native_snapshot_publication_root` and `native_image_staging_root` are set.
  vmmd then calls `JailerVMM.WithEnvironmentQualificationCapture()`.
- **The gate.** It opens only when the opt-in and every native prerequisite
  are present on the jailer. A default or partially configured host keeps the
  unchanged refusal before any effect.
- **Acceptance hosts.** The e2e harness sets the option only for
  `GREGALE_GITOPS_NATIVE_ACCEPTANCE=1`, so the native GitOps journey is the
  acceptance for capture, restore, smoke and retirement on that host class.
- **Production.** No production template sets it. Enabling it on a production
  host needs a passing native GitOps acceptance on that host class and an
  operator decision. The decision must also settle the shared snapshot
  directory, because the exclusive publication writer refuses
  `/srv/fc/snap`'s group-writable 2770 mode, the same open question as
  ADR 20261022184951.

## Consequences

- Acceptance runs can reach restore, smoke and retirement natively, and their
  result is recorded below.
- The option's own refusal at load time prevents a half-configured host from
  booting candidates that every capture would then reject.
- Production behaviour is unchanged until an operator sets the option.

## Amendment 1 (2026-10-24): restore delivers its receipt control over the resume hook

The first n2 run (`bindq-e1837337bf`) booted both candidates, passed the
source smoke and native capture, and retired both VMs. The restore then
failed with:

```
native restore load: verify original layer.ext4: storage: artifact differs from original receipt
```

Restore was staging pre-boot files (secrets, API env with a fresh receipt
token, resolver, CA) into the captured `layer.ext4` before load. Load
verifies that layer byte-for-byte against the capture receipt, so every
restore failed. Writing into a filesystem the restored guest already has
mounted is unsafe anyway. Also, guest-init emitted its configuration receipt
only at startup. A restored guest never re-reads the env file, so even an
unchecked layer could not have produced the restored-target receipt that
ADR-568 requires.

- **No staging on restore.** Restore stages no pre-boot file and unseals no
  secret. The layer stays identical to the capture, and the strict load
  check is kept.
- **A fresh control on the resume hook.** vmmd generates a fresh one-time
  token and MAC key per restore attempt. It sends them in the resume-hook
  body as `qualificationReceipt`, an `omitempty` key, over the connection
  that is already checked against the pinned original process. The waiter
  expects the restore request's frozen projection: the API env digest and
  the MAC of the secret key names.
- **The guest re-attests.** A qualification boot that emitted its receipt
  keeps only the receipt basis: the API env digest and the sorted secret key
  names, never secret values. After it acknowledges the resume, guest-init
  re-emits a receipt with the same encoding under the fresh control.
  - It retries for up to 30 s, because vmmd opens the restore's guest-event
    channel only after the acknowledgement.
  - A guest without a boot basis withholds the receipt.
  - A malformed control is ignored.
  - Both cases fail closed on the host.
- **The host waits while the channels are open.** The restore's private
  guest channels close when the native restore returns. A restored app is
  ready almost at once, so vmmd consumes the receipt inside the restore,
  after readiness and before the channels close. The first n2 run with this
  amendment (`bindq-0d1f6f3315`) showed the receipt leaving the guest
  113 ms after resume. vmmd then cancelled the stream when the channels
  closed, because it only waited after the restore had returned.
- **The expectation uses the capture's instance identity.** A snapshot
  restore cannot change the environment of a running process, so the
  restored guest keeps the `FAAS_INSTANCE_ID` it booted with. That is the
  capture instance's ID, while schedd builds the restore spec for the target
  instance. `FAAS_INSTANCE_ID` is the only per-instance entry: the restore runs
  under the same request and graph, on the same node, so binding URLs match.
  - vmmd expects the restore request's env with that entry set to the
    capture instance.
  - schedd records the same digest for the restored target
    (`fcvm.QualificationRestoreAPIEnvSHA256`).
  - The n2 rerun with the in-restore wait had the receipt rejected
    (`did not match this attempt`) until this change.
- **What the receipt proves.** A capture made from different inputs cannot
  satisfy the restore request's expectation. Restore still refuses sidecars,
  so only the main workload re-attests.
- **Version skew.** An older guest ignores the new key and sends no receipt,
  so the restore fails closed instead of qualifying without proof.

## Acceptance record

- 2026-10-23, n2 `bindq-e1837337bf`: capture passed; restore failed at the
  layer verification described in amendment 1.
- 2026-10-24/25, n2 `bindq-0d1f6f3315`:
  - The Linux unit tests passed.
  - The restore loaded the capture with the strict layer check, and the
    resume hook was acknowledged.
  - The receipt was dropped as described above.
- 2026-10-25, n2, rerun with b5819ddd5a on a private PostgreSQL (the shared
  e2e cluster was down after a full root disk):
  - The gitlab restore received the guest's receipt inside the restore.
  - vmmd rejected the receipt because of the instance identity mismatch
    described above.
  - The github capture failed on a full scratch disk.
- Pending: the n2 native GitOps journey with the capture-identity
  expectation.

## Rejected alternatives

- **Remove the gate.** That would open capture on every native host before
  any acceptance.
- **Tie it to `native_runtime_capture`.** Runtime capture and qualification
  capture have different authority and retirement paths, and each needs its
  own acceptance.
