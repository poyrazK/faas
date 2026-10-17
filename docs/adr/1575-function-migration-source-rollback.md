# ADR-1575 · Recover a function migration source after rollback

- **Status:** implementation for qualification; native execution pending; production off
- **Date:** 2026-10-17
- **Decision:** Add lease-bound source preparation and rollback recovery for the
  qualified Node resource lifecycle. Keep destination adoption and measured
  migration capture refused.
- **Why:** Resuming a paused source is insufficient after its PostgreSQL pools
  were drained. A rollback also needs a durable fence against a concurrent
  ownership commit before rebuilding pools or admitting another request.

## Ownership and recovery

Only `metal,function_background_native` builds with the trusted execution
receiver can prepare this source. A source record binds the migration token,
source node and original wake. Capture handles become durable in the same
registration statement before checkpoint effects. Source tokens have a versioned
prefix; initial migration marking requires a prepared, unexpired receipt and
cannot fall back to legacy marking after cleanup. The host additionally pins the
complete physical incarnation. Ordinary snapshot, resume, freeze and invocation paths cannot open
the prepared source. All loaded interpreters drain before the source pauses;
it stays paused through publication and while the lease is outstanding.

schedd remains the only instance-row writer. Before cancellation or expired-lease
recovery it locks the source attempt and the instance ownership row. It replaces
the original migration token with a rollback token while retaining MIGRATING.
The original ownership commit can no longer succeed. The same transaction handles
the pre-Phase-2 window, so expiry cannot race a delayed initial mark. Ambiguous
ownership grants no permission to resume or destroy a replacement.

vmmd reads that durable fence before source recovery. Under one destruction-joined
instance flight it resumes the original guest, rebuilds and probes every pool,
and waits for the single-attempt recovery ACK. Duplicate cancels and expiry
retries reuse the completed result instead of replaying application callbacks.
schedd returns the same source wake to RUNNING only after confirmation. Cleanup
of temporary objects and the durable lease follows the ownership completion.

A failed probe, lost ACK, cancelled destruction-joined recovery or unfinished
checkpoint seals admission and retires the original physical VM. Unconfirmed
retirement retains allocator, cleanup and scheduler capacity ownership. The wire
distinguishes a confirmed retired source from a recovered one, so schedd can
complete STOPPED without falsely publishing RUNNING. Qualified cancellation
requires an explicit source protocol version; an older empty response is not
recovery evidence. A committed handoff permits retirement of the original source, never its resumption.

## Boundaries

This is source rollback qualification, not a destination rollout. Migration
adoption rejects captures with a function-source record. Managed/measured
migration capture still requires fresh capture, destination and cancel/resume
authority and remains refused. A vmmd restart without the original in-memory
physical owner does not reconstruct an empty resource ledger or run callbacks.
Existing native recovery quarantine and durable records retain uncertainty.
Ambiguous registration acknowledgements retain the lease and a known no-effects
host result. An absent durable source receipt grants no generic resume/retire
authority. Admission repair retains referenced source reservations even when
legacy rollback temporarily cleared the instance token.

Normal production builds, Python, application SQL, plan entitlements and billing
are unchanged. SQL and application requests are never replayed. Native KVM,
real-vsock failure/restart, private-environment, leak and performance acceptance
remain required before any activation change.

Follow-up: [ADR-1576](1576-function-migration-admission.md) adds dedicated capture
and recovery permissions for measured sources in the qualification build. It
supersedes the measured-source refusal above while retaining the destination,
native acceptance and production activation gates.
