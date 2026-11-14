# ADR-20261104111500: Concurrent repair capture and durable outcome recovery

- Status: accepted for internal operator qualification
- Date: 2026-11-04
- Extends: [catalog contracts](20261103224500-repair-catalog-contracts.md) and [apply resolution](2026101703-repair-apply-resolution.md)

## Problem

The enrollment's transaction-long SHARE UPDATE EXCLUSIVE lock serialized all
captured writers, even when they updated different records. Separately, applying
an inverse through individual UPDATE statements could not reverse a valid swap
under a deferrable, initially immediate unique or exclusion constraint.

Hosted operations in `outcome_unknown` could expire and be pruned before their
source commit outcome was resolved. A late recovered success could also retain
an already elapsed result window and become immediately inaccessible.

## Decision

For READ COMMITTED capture and repair, take ROW EXCLUSIVE before inspecting the
enrollment. This lock remains held through commit and fences ordinary index and
table DDL while allowing independent captured writers. Before the first capture
in this transaction for the relation, reject another backend's SHARE UPDATE
EXCLUSIVE maintenance lock with a retryable conflict. Inspect current database
lock-manager state, including waiting locks, rather than an MVCC catalog snapshot.

PostgreSQL 16 concurrent index creation and reindexing retain their maintenance
lock across phases. A build already running is rejected before business work.
A build started after our check must wait for existing ROW EXCLUSIVE writers
before its first build scan; it cannot activate a new index ahead of this
transaction. Later qualification in this transaction recognizes its immutable
capture operation metadata through a transaction-local lookup key validated
against its identity, enrollment and transaction ID. The lookup uses the journal
primary key rather than scanning historical operations. Finalization does not deadlock with maintenance
waiting for that same writer. Qualification still rejects unsupported index
definitions and repeats filenode, fingerprint and type checks through commit.
REPEATABLE READ and SERIALIZABLE callers keep the prior stronger table lock.
All three SDK receipt wrappers already select READ COMMITTED explicitly.

Lock all inverse candidates in their existing deterministic order and validate
every version and saved value before writing. Restore the complete selection in
one UPDATE statement. Each row reconstructs its own partial inverse using the
native row as the base, preserving fields absent from that inverse. Repeat
identity/version predicates in the UPDATE and require the exact candidate count.
Existing immediate-constraint validation and business checks still precede
commit. This supports deferrable statement-level swaps without relaxing
nondeferrable constraints or rewriting saved plans and approvals.

Retain hosted identities, events and idempotency records while a repair is
`applying`, `outcome_unknown`, `replacing` or `resolving`. These states cannot
expire into permission to retry or replace an unresolved apply. Once a durable
receipt or closure settles apply, restart ordinary result/event retention without
refreshing source plan expiry. Settled operations remain eligible for normal
pruning after that window.

Install the application-database changes with the explicit owner schema upgrade;
no reenrollment, new capture provenance or historical evidence rewrite is needed.
Writer and executor roles need SELECT on `pg_catalog.pg_locks`, which PostgreSQL
normally grants to PUBLIC. Readiness reports a missing grant if it was revoked.
Regenerate all SDK probes with the matching installed function manifest.

## Validation

PostgreSQL tests commit independent captured writes while another capture is
open, reject an already running concurrent index build, allow a later build to
finish after capture commits, and verify ordinary table DDL remains fenced.
Unique and exclusion swaps exercise full apply, native verification, partial
inverse preservation and optional real Docker rehearsal. Memory and PostgreSQL
store tests preserve unresolved identities/events across pruning, recover late
receipt/closure outcomes, and then verify normal expiry resumes.
