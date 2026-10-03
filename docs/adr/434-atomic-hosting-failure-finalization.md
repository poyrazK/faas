# ADR-434: Atomic hosting failure finalization

Status: accepted

Date: 2026-10-03

## Context

The post-readiness verifier writes a hosting receipt before imaged promotes or
fails a deployment. Failure handling previously ignored receipt and status
write errors, then notified consumers that the candidate failed. A persistence
outage could therefore leave a failed row without its verdict, or publish a
failure for a candidate that remained snapshotting. A successful probe followed
by a receipt write outage also caused a terminal failure.

## Decision

Commit a failed receipt together with the existing deployment failure
transaction: status, error, active stage closure, traffic rebalance, lifecycle
webhooks and organization outcome activity. PostgreSQL locks the candidate
row before checking its state. MemStore mirrors the same critical section.
Only snapshotting candidates can receive a new failure verdict. Cancelled,
failed, superseded and live deployments retain their established outcomes.

Validate the receipt's failed verdict and deployment/app identity before
writing. Notify routing consumers and record terminal failure metrics only
after a successful commit, and only for the caller that changed the row.
Duplicate or stale finalizers cannot overwrite evidence or republish failure.

Return persistence errors to the existing durable snapshot_written consumer;
leave the candidate nonterminal and its predecessor serving. A failed write of
a successful verification receipt follows the same replay path. Promotion
continues to require durable evidence before changing the live pointer.

Parent-context cancellation is an interrupted consumer, not an application
verdict. Preserve cancellation as an error and allow replay with a fresh
context. An exhausted probe budget with an active consumer still produces the
existing failed-verification verdict. An unsupported atomic failure store
fails closed rather than falling back to separate writes.

## Consequences

No new deployment states, database columns, retry queue, public API fields,
quotas, or VM lifecycle behavior. The existing deployment notification outbox
owns retry and crash recovery. Previously finalized rows remain immutable to
this operation, while the general SetDeploymentFailed contract is preserved.

TestHostingFailureCommitsVerdictAndOutcome pins the store parity contract.
TestPg_HostingFailureRollbackIsReplayable injects failures during update and at
commit. Imaged replay tests verify event ordering, failed and successful
receipt outages, consumer cancellation, and a customer cancellation winning
against a stale verifier.
