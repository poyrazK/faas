# ADR-20261104140000 · Fence rollback execution and reconcile outcomes

- **Status:** accepted
- **Date:** 2026-11-04
- **Amends:** [ADR-1446](1446-safe-releases-by-default.md),
  [ADR-1448](1448-releases-that-change-the-database.md),
  [ADR-20261023181931](20261023181931-rollbacks-that-restore-the-database.md),
  and [ADR-20261104110000](20261104110000-rollback-release-and-contract-evidence.md).
- **Decision:** Validate a database switch's lease inside code mutations,
  persist plain rollback release reviews with their preparation attempt, retain
  contract checks during project rollback, and reconcile outcomes under the
  target lock with fair bounded sweeps.

## Execution

A database-switch step checks its lease before running and is bounded by its
remaining lease time. Preparation and promotion recheck ownership after reads
and pass the switch identity, scope, phase and token into the native store.
PostgreSQL locks the switch row before checking its expiry and keeps that lock
through the traffic transaction. MemStore validates ownership under its mutation
mutex. A replacement worker cannot finish an abort and then have its result
undone by an old worker's code write. Cutover and provider operations retain their
existing idempotency and state checks.

Plain rollback preparation records exactly which release runs were acknowledged.
The additive `deployments.rollback_release_review` column binds those runs to the
marker epoch. Updating the marker with an older binary invalidates the old review
rather than reusing consent from another attempt. Missing reviews acknowledge
nothing. Native activation validates fresh release history under the same parent
lock that serializes release-task starts. A current database-switch execution
lease authorizes its separate database-restoring workflow; expired leases do not.
Late migration refusals fail the plain operation with its existing stable code.

Synchronous project rollback compares the retained contract with its selected
serving deployment before publication. It retains strict historical contract
checks, without route-removal exceptions. Its conditional native traffic write
also refuses unacknowledged release crossings.

## Outcomes

Finishing a failed plain rollback locks and reads the target again. If activation
has completed, it leaves the receipt preparing for reconciliation instead of
publishing an incorrect timeout, audit or alert. The next sweep records the
actual completion. Existing completion timestamp semantics are unchanged.

A worker retains a keyset cursor across bounded outcome sweeps. Still-preparing
or temporarily unreadable targets do not prevent newer receipts from being
visited. Reaching the end wraps to the oldest pending request. Concurrent sweeps
on one worker serialize; conditional finish still arbitrates APID replicas.

## Rollout and validation

Apply the additive migration before updating control-plane or imaging binaries.
No customer request fields or SDK changes are required. Regression coverage uses
MemStore and PostgreSQL for migration reviews, traffic leases, concurrent outcome
resolution, bounded queue traversal, and project contract protection, with normal
rollback and database-switch controls alongside the reproduced failures.
