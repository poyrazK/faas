# ADR-20261104202549: Pipeline source storage checks and missing-slot cleanup

- **Status:** implemented for local/staging qualification; production disabled
- **Date:** 2026-11-04
- **Amends:** ADR-1591, ADR-20261103220000

## Context

A source heap rewrite can change stored values without changing logical column
metadata or producing normal row replication events. A live mirror could keep
serving obsolete values while reporting current replication health. Verification
already fences individual proofs against rewrites, but ordinary reconciliation
had no durable storage baseline.

Cleanup recreated a missing main replication slot before native subscription
deletion. If unrelated slots exhausted publisher capacity, deletion could not
complete even though it needed no new replication stream.

## Decision

Persist opaque source storage fingerprints in the owned destination mirror
receipt. New mirrors record them before native copying starts. Successful sealed
proofs advance fingerprints for their verified scope while preserving other
selected tables' baselines. Existing mirrors without these receipts must qualify
their imported data instead of silently adopting the current source version.

Ordinary reconciliation checks this baseline before exposing reads. A changed
fingerprint closes all selected read policies and starts the existing bounded,
resumable full verification. Harmless rewrites can reopen reads after equality
is proved under source locks and the current worker-intent fence. A mismatch
requires explicit resync, closes reads and retains imported rows. Cancellation
and work limits retain the gate; a fresh stopped proof can use the existing
verification retry control. CLI waits continue while qualification is pending
and stop on its terminal results.

Reuse the private schema receipt's verifying phase with a distinct, non-approval
marker while the storage gate is active. Older workers reject this marker before
reopening reads. Replay a successful proof from its private checkpoint when its
catalog acknowledgement is lost. Clearing the marker, recording the
qualified fingerprints and reopening reads use a destination transaction fenced
by the locked source storage and current intent. A rewrite of a healthy baseline
table during scoped addition, repair or removal requires explicit resync rather
than embedding an unrelated full proof within that workflow.

For a disabled, ownership-checked subscription whose main slot is missing, detach
the main slot and issue native DROP SUBSCRIPTION in one destination transaction.
PostgreSQL still removes its table-sync slots. It may tolerate a publisher
connection failure when no main slot is assigned, so query the source afterward
and refuse to commit while any matching native table-sync slots remain. Match
the subscription OID and subscriber system identifier, covering every relation
OID without confusing slots from another subscriber cluster. Never drop matching
slots manually or mutate unrelated slots.

Check the current catalog intent before committing. A failed cleanup or lost
lease rolls back detachment and deletion together, preserving the subscription's
original ownership identity. Remote slot removal cannot roll back; native cleanup
already tolerates missing sync slots when the same attempt is replayed. Existing
main-slot cleanup keeps using native DROP directly. Neither path allocates a
replacement replication slot.

## Validation and rollout

Native tests cover value-changing rewrites, harmless VACUUM FULL, legacy receipts,
lost proof acknowledgement, source DDL fencing, lost worker intent, cancelled and
limited proofs with explicit retry, and healthy-table rewrites during removal.
They verify gated signed reads and preservation of destination storage and
subscription identity after successful qualification.

Cleanup acceptance fills all publisher slot capacity with unrelated slots, both
with and without an unfinished native table copy. It verifies successful deletion,
native sync-slot removal, retained imported tables and untouched unrelated slots.
A failed native publisher connection and a rejected final intent fence verify
atomic ownership recovery and replay after nontransactional remote cleanup.

No catalog migration is needed; the owned private receipt expands lazily. The
combined branch retains its existing setup-ownership migrations. Production
admission remains disabled pending deployed qualification.
