# ADR-20261104211004: Pipeline native stream continuity and readiness

- **Status:** implemented for local/staging qualification; production disabled
- **Date:** 2026-11-04
- **Amends:** ADR-1591, ADR-20261104202549

## Context

A source slot recreated under its original name can skip committed writes while
the subscriber subsequently applies newer changes and reports current progress.
The slot's name, plugin, database and WAL health do not establish continuity.
PostgreSQL 16 exposes no stable slot incarnation identifier; its statistics reset
timestamp is normally null and cannot identify creation.

Ordinary reconciliation counted ready relations without validating the complete
native relation set. Removing a previously ready table's registration left stale
rows readable, reported copying forever and prevented direct resync admission.

## Decision

Keep a source stream witness in the owned private mirror receipt: the active
sender's PID/start time and its last observed confirmed WAL position. An active
sender holds its slot and prevents replacement. After that sender changes, allow
continuation only when retained WAL overlaps the preceding observed checkpoint.
Do not advance this checkpoint while the sender is stopped. A current target
origin or heartbeat cannot establish historical continuity after a skipped gap.
Before a controlled pause, briefly refresh an already trusted sender's boundary
so saved manual verification can resume unchanged. This optional bookkeeping has
a bounded budget and cannot prevent pause when source metadata is unavailable.

Before creating a subscription, lock selected source tables against writes and
save its initial WAL witness. Briefly wait for and record its first main sender
under those same locks, with the current source/lease fence. Native copying can
read while the locks are held. A lost creation acknowledgement or an unobserved
first sender does not authorize blind adoption later. Initial reads require a
recorded sender and complete native readiness. This preserves native initial-copy
support for rows exceeding optional verification limits.

An existing stream with uncertain continuity closes imported reads and enters
the existing resumable full verification using reason
`replication_continuity_verification`. Record a fresh private epoch in the same
transaction as the durable schema-phase read gate. Include this epoch in physical
proof fingerprints, invalidating old passed proofs and ranges even if table
storage is unchanged. Retain logical intent identity so cancellation remains
effective. Recheck stream continuity before sealing and acknowledging equality.
Use a distinct continuity gate identity so older storage-only workers reject
that receipt before opening reads.
An approved full-schema workflow can restart its existing all-table proof in
the new epoch. Preserve its schema approval and API-refresh phases, and enforce
a private receipt constraint that forbids readable state while continuity is
pending. Older schema workers cannot release that constraint; the new worker
clears pending only in the transaction completing an all-selected-table proof.

A successful proof reopens reads without replacing tables or the subscription,
and clears the pending continuity marker atomically. A mismatch reports
`replication_continuity_lost` and requires explicit resync while retaining imported
rows. Stopped and limited proofs stay gated and can explicitly retry verification;
CLI waits continue through pending qualification. A shared continuity failure
during a scoped workflow requires resync rather than admitting an unrelated full
proof into its approved scope.

Validate every native relation's schema, selected name, ownership and state, and
require the exact selected set even during initial copy. Persist the first fully
ready boundary independently of catalog acknowledgements and read policy state.
Once reached, a missing or non-ready relation is `replication_state_lost`, closes
reads, retires native resources and offers explicitly confirmed resync. Never
implicitly register a missing table with `copy_data=false` or clear imported rows.

## Validation and rollout

Native regression tests remove a ready table while another keeps syncing, verify
read closure and retained rows, then complete explicit resync. A separate case
loses catalog readiness and closes reads before a ready relation regresses into
copying, checking the private completion boundary.

Slot tests recreate a paused stream's slot, including a case where its newer
stream applies changes before the worker polls. Missing historical changes require
resync; unchanged data requires a new proof and preserves table/subscription
identity. A retained-slot reconnect uses the minimum source role permissions and
resumes without unnecessary verification. Legacy private receipts qualify rather
than reuse a proof from before the continuity witness.

No catalog migration or new source privilege is required. The private receipt
expands lazily; sender metadata is visible to its own source role. Existing setup
migrations remain required for the combined branch. Production admission stays
disabled pending deployed qualification.
