# ADR-1669: Read-only mutation receipt recovery

Status: Accepted

## Context

The Data SDK identifies uncertain writes after a timeout, cancellation, oversized
response or lost connection. The starter's note form previously recovered by
replaying its idempotent creation RPC with the same key. That can confirm a
retained receipt, but a replay after owner receipt purging can create another
note. Applications need to check committed receipts independently of write
replay, while preserving the original operation and its authenticated scope.

## Decision

Add append-only customer migrations for `read_note_create_receipt` in the starter
and `read_order_receipt` in the idempotent-order example. Both are explicitly
annotated, protected `STABLE SECURITY INVOKER` functions with
`search_path=pg_catalog` and no public execution. Use the existing owner permission
and schema-refresh workflows. No platform endpoint or new connection pool is
introduced; these approved RPCs use the existing authenticated POST RPC route.
Their SQL performs no mutation, key reservation, advisory locking or replay and
works inside server-enforced read-only transactions.

Derive subject and, for orders, tenant from the verified request claims. Match
the exact operation key and compare all operation-defining arguments with the
stored request. Return the original immutable response as one row. A visible
payload mismatch returns HTTP 409/SQLSTATE `PT409` with the fixed message
`idempotency_payload_conflict`. An absent, uncommitted, purged or RLS-hidden receipt
returns no rows. Hidden receipts do not reveal payload conflicts. Neither RPC
accepts an identity selector or returns private receipt internals.

Add a SELECT-only scoped RLS policy for note receipt lookup; preserve the existing
writer policy. The invoker sets its transaction-local lookup scope and restores
the previous value on success or error. Direct table access stays hidden. The
order example reuses its existing subject/tenant RLS and immutable order receipt.
The first slice covers database effects only; external side effects continue to
need their own idempotency or outbox contract.

Export `reconcileMutation` and `MutationReconciliation` from `@gregale/data`.
The helper classifies an already completed receipt lookup and executes no query,
polling or retries. Require a synchronous application predicate to match the
receipt to its pending operation's identity and payload. Preserve selected row
types, original request errors and status. Return `confirmed` only for one
matching object row, `payload_conflict` only for the precise server conflict
contract, and `unresolved` otherwise. Distinguish missing receipts, request
failures and invalid/ambiguous receipts. Do not include rejected rows or predicate
exceptions in an unresolved result. Empty results never establish rollback.

Change the starter form's `reconcile()` to receipt lookup. Bind confirmation to
its captured subject and pending note body. Preserve the frozen original key and
payload on every unresolved lookup, conflict, malformed response or identity
mismatch. Expose explicit `retry()` separately; failed retries preserve the
original uncertain operation even when the latest request is refused. Browser
actions distinguish checking a pending save from retrying the original save and
require explicit review of duplicate risk before replay. Initial save and retry
responses must also match the controller's subject before clearing pending state.
No credentials or pending operations are automatically persisted.

Update the typed order example with read-only reconciliation bound to its original
subject, tenant, key and total. Applications retain their original API/identity
context, key and full payload and decide explicitly whether to retry. Do not infer
safety from a missing receipt. New migration fixtures follow the new starter
version; previously applied migrations remain unchanged.

## Consequences

Applications can confirm a committed operation without executing it again.
Receipts remain authoritative only under the application's retention and key-use
contract. Purging ends that guarantee; a later reused key cannot identify a
removed receipt. Lookup failures, hidden records and in-flight work remain
unresolved. Confirmation describes the original committed response, which may
differ from a currently edited or deleted business row. Callers may compose
existing runtime response validation when stronger row validation is needed.

## Validation

SDK unit and inference tests cover exact selected types, scope/payload matching,
conflicts, empty/malformed/ambiguous results, sensitive predicate failures and the
absence of polling or query execution. Starter tests preserve pending state on
lookup errors, identity changes and refused retries. Native PostgreSQL/PostgREST
tests exercise lost responses, one committed result, uncommitted work, purged
receipts, original snapshots after edits/deletion, subject/tenant isolation,
read-only transactions and RLS scope restoration. Existing permission readiness,
migration integrity, browser recovery and development/upgrade fixtures remain
regression coverage.
