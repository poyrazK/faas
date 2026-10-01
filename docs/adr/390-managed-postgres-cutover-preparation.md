# ADR-390 · Managed PostgreSQL cutover preparation

- **Status:** accepted
- **Date:** 2026-10-01
- **Decision:** Persist a grouped cutover preparation intent, stage encrypted
  target credentials privately, and fence its database and binding dependencies
  until preparation is cancelled and every possible target identity is revoked.
- **Why:** Restore-to-new-database works, but deleting a serving binding to
  attach the restored target creates a credential gap. A recoverable cutover
  needs target credentials prepared before any workload configuration changes.

This first slice supplies `CutoverService` preparation/status/cancellation and
background recovery through the binding reconciler. It has no public cutover
route, CLI activation command, or workload transition. `prepared` means all
credentials are sealed; it does not prove SQL connectivity or authorize writes
to the restored target. Writer draining and activation remain separate work.

An intent selects all active source bindings for one app and deployment scope,
including runtime and migration access. The restored target must be a ready,
unbound direct restore of that source with matching recorded source identity.
Both resources and all selected bindings must be in the same account. Existing
rotations or lifecycle operations cause a conflict. Reservation locks account,
app, database rows in ID order, then binding rows in ID order. It persists the
whole member set and resource/generation/fingerprint pins before provider I/O.
One active intent per app/scope and dependency pins prevent overlapping work.

Nullable `cutover_id` columns on both databases and the source bindings fence
rotation, deletion, placement/spec changes, and new bindings through PostgreSQL
triggers, including callers outside the new service. The database locks serialize
reservation with existing binding/restore/deletion ownership boundaries. Pinning
both databases is intentionally conservative: other apps cannot attach new
bindings to either database while preparation is active. Existing bindings in
other apps retain their current behavior. Transaction deadlocks or serialization
failures return the existing conflict sentinel and may be retried.

Each member receives a new immutable future target binding ID and generation 1.
These IDs determine provider credential issuance and revocation identities.
They differ between cancelled and subsequent intents, avoiding revival of a
retired staged identity. The future activation stage must publish those IDs as
new bindings, rather than deriving a different credential identity from an old
source binding generation.

`CredentialSealer` creates the existing age envelope, recipient ID, value HMAC,
and opaque reference without calling `PutManagedPostgresSecret`. The envelope
lives only in `managed_postgres_cutover_credentials`, never `app_secrets` or
customer responses. Binary ciphertext matches the existing secret transport.
Provider and sealer errors become stable codes; plaintext has only the bounded
in-process credential lifetime. A failed catalog commit leaves the lease to
expire; retries issue the same identity and seal it again. Completion verifies
a live lease and the current preparation/cancellation state transactionally.
Preparation performs at most one member operation per intent per sweep.

Cancellation retains a running worker's lease. It revokes every member,
including pending members whose provider issuance may have succeeded without a
catalog commit. Only after all revocations succeed does it erase staged envelopes,
mark the intent cancelled, and atomically release every dependency pin. Cleanup
runs with provisioning disabled. If recovery encounters an already-deleted app
or deleted-pending account, it cancels the intent automatically. Existing app
and account deletion guards still reject deletion while resources are live.
Cancellation cannot publish or alter a source application secret.
Provider operations retain their existing bounded, idempotent contract.

Source binding deletion cascades completed credential history; active pins
prevent deletion until cleanup completes. Cancelled history therefore cannot
block later account erasure.

Prepared intents retain their pins until explicit cancellation; there is no
silent expiry that might strand provider credentials. Rollback refuses active
intents. Operators cancel and let cleanup complete before reverting the schema.
The existing host-key maintenance path covers app_secrets only; prepared envelopes
must be cancelled/reprepared before retiring an age identity needed to decrypt
them. A future activation implementation must verify recipient availability.

Validation covers grouped runtime/migration staging, no secret publication,
idempotent concurrent native reservations, cross-account isolation, rotation and
deletion conflicts, late-worker and lease recovery fences, revocation after an
uncommitted issuance, cleanup with the gate closed, and migration down/up safety.
