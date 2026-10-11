# ADR-591: Managed PostgreSQL legacy accounting reconciliation

Status: accepted · 2026-10-05

## Context

ADR-581 deliberately leaves deleted legacy resources with missing identities
unresolved. Their old logical deletion timestamp does not establish provider
shutdown. ADR-582 diagnoses this condition, and ADR-583 refuses usage imports
without an identity. Operators need an evidence-backed repair before recovery.

## Decision

Add operator-only preview and apply under
`/v1/admin/managed-postgres/accounting/{account_id}/reconciliations`, with preview
at `/preview`. Apply uses the existing allowlisted operator session, recent MFA,
same-origin and Idempotency-Key mutation policy. Preview performs no writes or
provider calls. Reconciliation is limited to deleted, accountable resources
whose provider identity is still missing and whose lifecycle lease is inactive.

The operator attests the exact immutable backend ID/fingerprint, provider resource
identity, actual shutdown time, source observation time, retained evidence
reference/SHA-256 and reason. The service does not fetch or authenticate artifacts.
The operator must verify resource ownership, branch lineage and actual shutdown;
absence from a lookup and the old logical deletion time are not sufficient proof.
References must contain no credentials or signed URLs. Evidence times must use
microsecond precision; shutdown cannot precede catalog creation or exceed its
observation, and observation cannot be in the future.

Preview fences the catalog, policy, derived coverage and relevant ledger bounds.
Apply recomputes under the collector's database row lock. It rejects a provider
identity already attached to another catalog row in the same immutable backend,
or claimed by another retained reconciliation. A transaction-scoped identity
lock and receipt uniqueness serialize concurrent operator claims. Provider
identity aliases across different backend configurations still require the
separate provider-account identity model; an operator must verify that mapping.

Replace the legacy `deleted_at` with the attested actual shutdown boundary and
attach the identity atomically with an immutable before/after catalog receipt.
Retain the old logical deletion timestamp in that receipt. Never clear
`accounting_required`, revive a resource, or modify monetary ledger rows.
Reset derived coverage in the same transaction so existing checkpoints alone
cannot settle the repaired resource. Reject incompatible windows, ledger rows
outside the attested lifetime, or evidence older than retained observations.
Automatic collection or reviewed usage imports must reestablish contiguous
coverage and final corrections before admission resumes.

Shared restore descendants retain their existing lineage. They cannot acquire
independent ledger rows or import aggregate usage. After identity reconciliation,
the collector reattaches shared coverage to the existing accounting root; unresolved
roots remain blocking. Reconciling a root resets its coverage and therefore its
descendants' effective coverage as well.

`(account_id, reconciliation_id)` permanently deduplicates identical requests by
the same actor, including after policy changes. Conflicting reuse is rejected.
Only one receipt can repair each database or claim each identity within a backend.
Receipts are append-only while the account exists; final account erasure cascades
them through deferred catalog foreign keys. Schema replay preserves receipts and
reinstalls audit protection. Rollback refuses to discard retained evidence.

Expose API/Go/Node/Python SDK methods and
`gregale postgres reconcile ACCOUNT_ID --file FILE --json` for preview;
`--apply --session-file SESSION_FILE` applies a reviewed `expected_revision`.
The result describes the committed repair and required recovery, not current
admission status on later receipt replay. Use diagnostics to check live coverage.

## Validation

Memory and PostgreSQL regressions cover preview without writes, repaired shutdown
boundaries, coverage reset with ledger retention, import/collector recovery,
shared roots, actor/request replay, conflicting identity claims, concurrent
collection, cancellation, audit failure rollback, migration replay and account
erasure. HTTP/CLI regressions enforce strict decoding and operator authentication.
No provider mutations or VM lifecycle changes are introduced.
