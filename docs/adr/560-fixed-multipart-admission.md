# ADR-560: Atomic fixed multipart admission

Date: 2026-10-04
Status: Accepted

## Context

Control multipart creation reserves quota before persisting its session. A
session-limit failure leaves an unrelated legacy key grant, concurrent retries
spend multiple authorizations, and all-version accounting rejects this untracked
reservation. Bucket encryption defaults need a single authoritative admission
that binds policy, declared bytes and native initialization.

## Decision

Reserve a fixed control multipart session and its full-object write admission
atomically under account and bucket serialization. Bind the admission identity
to the session and declared key/size. New fixed sessions persist a private
immutable flag and database guards require their matching admission at commit.
Take the bucket's non-key update lock before the account lock, matching
configuration/lifecycle transactions while allowing account-locked tracked
writes to acquire bucket foreign-key locks.
Retries reuse the accepted session without another reservation or authorization.
Quota and session-limit failures publish neither session nor grant.

The admission captures current or all-version accounting. Fixed part URLs
continue using the full-object reservation; completion reuses that admission
instead of allocating a second native version. Abort and completion retain the
existing provider-proof and inventory-reconciliation requirements. Legacy fixed
sessions keep their conservative accounting and recovery contract.

## Consequences

Control multipart creation supports version-accounted buckets and can later
capture an encryption default in the same transaction. Accepted session shape
and quota identity cannot be rewritten by older workers. Rollback requires
draining new fixed sessions. Other S3 gaps remain separate increments.

## Acceptance

Memory/PostgreSQL tests cover atomic quota/session/ownership/layout failures,
concurrent creation replay, retained-version overwrite limits, completion
reservation reuse, legacy replay, verified abort/inventory reclamation and
durable restart. PostgreSQL tests exercise configuration/account/FK lock order,
older-writer deletion/mutation guards and deferred admission binding. Migration
tests cover legacy upgrade, blocked live rollback, drained rollback with retained
capacity and cold reapply.

Local control API → part broker → native S3 HTTP tests cover both current and
all-version inventory profiles, exact owned encryption, grant revocation,
sequential part replacement, lost completion acknowledgment and restart recovery
with disabled KMS. Completion and replay retain one object/version reservation.
Focused state/API race tests, related state/migration/API regressions, full
provider/gateway suites, public Go SDK tests, OpenAPI compliance/synchronization,
schema/SQLC parity, repository gates and zero-issue changed-line lint pass. Public
API schemas are unchanged. No live provider, deployment or push is required.
