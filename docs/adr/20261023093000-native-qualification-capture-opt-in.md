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

## Acceptance record

- Pending: the n2 native GitOps journey run with this option.

## Rejected alternatives

- **Remove the gate.** That would open capture on every native host before
  any acceptance.
- **Tie it to `native_runtime_capture`.** Runtime capture and qualification
  capture have different authority and retirement paths, and each needs its
  own acceptance.
