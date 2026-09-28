# ADR-351 · Customer callback before terminal init capture

- **Status:** accepted
- **Date:** 2026-09-28
- **Decision:** Request and service apps may opt into `before_checkpoint` in
  lifecycle configuration. The effective app manifest is baked into each
  deployment artifact. For a new terminal init capture, vmmd asks guest-init
  to POST to the app's loopback listener after the optional pre-snapshot
  extension notification and before pausing Firecracker. Only a 2xx response
  lets capture continue. A missing guest setting, HTTP failure, timeout, or
  transport error aborts capture and the park cleanup destroys the VM.
- **Bounds:** The default callback deadline is 500 ms and the maximum is 2
  seconds. The guest sends `X-Faas-Before-Checkpoint: 1`, no body, never
  follows redirects, and ignores the response body. The host waits at most
  3 seconds within the existing snapshot budget. A failed capture may be
  retried, so the callback should be idempotent.
- **Snapshot tiers:** The setting disables warm captures for that app. Warm
  capture resumes the source guest, which may have closed connections in the
  callback. Migration capture never calls the callback. Reusing an existing
  init snapshot never calls it either; changing the setting invalidates all
  existing snapshot tiers and retires running guests with the old manifest.
- **Compatibility:** Omitted configuration preserves existing capture
  behavior. `before_checkpoint: {}` in a PATCH clears the setting. The
  changed callback is baked into a new deployment; an older artifact has its
  old guest configuration. No database migration is needed because the app
  manifest uses existing JSONB. The vmmd request and response add optional
  protobuf booleans. A new schedd requires explicit callback completion from
  vmmd before publishing an opt-in capture, so an older vmmd that ignores the
  request cannot silently publish it.

The guest callback is a required snapshot barrier. The existing extension
notification is best effort and cannot provide this guarantee. Native KVM
acceptance must prove a successful callback precedes the pause, a failed
callback publishes no snapshot, and the guest is cleaned up after failure.

## Failure diagnostics

The guest's rejected callback ACK becomes the stable
`before_checkpoint_failed` code across vmmd and schedd. Snapshot prime
stores that code and customer guidance on the failed deployment. A later
park retains its existing `park_snapshot_error` audit kind and uses
`before_checkpoint_failed` as its closed reason. Guest logs keep the HTTP
or timeout detail; deployment and audit records carry no callback response
body or host path. Other snapshot failures retain their existing codes and
reasons.

The per-wake timeline closes a failed terminal capture with
`wake.park_failed`, carrying the same closed reason and start/failure times.
The CLI renders only known reason values. The existing
`wake.park_completed` event remains exclusive to successful captures.
