# ADR-1671: Versioned update and delete receipts

Status: Accepted

## Context

V2 note creation rejects expired keys even after receipt cleanup (ADR-1670).
Conditional updates use row versions, but a lost response followed by a version
conflict cannot identify who performed the earlier edit. A missing row likewise
cannot confirm which request deleted it. Successful updates and deletes need
their own immutable receipts for recovery independent of current business state.

## Decision

Add append-only starter migration `0013_note_mutation_receipts.sql`, leaving
earlier migrations and ordinary REST methods unchanged. Expose approved invoker
RPCs `update_note_once_v2(uuid,integer,integer,jsonb)` and
`delete_note_once_v2(uuid,integer,integer)`. Arguments are operation key, note ID,
expected version, and for update a patch. Only nonempty patches containing
`body` (non-null string) and/or `priority` (PostgreSQL integer) are accepted.
Identity, row version and generated fields cannot be patched. Invalid mutation
arguments return `PT400` / `idempotency_mutation_invalid`.

Derive subject from verified JWT claims and preserve all table RLS checks. Keep
successful receipts in `api.note_mutation_receipts_v2`, with a primary key on
subject and operation key. Updates and deletes share this namespace. Record the
action, target ID, expected version and full patch in the canonical JSON request.
Creation retains its separate namespace. Applications must preserve the original
API, identity, contract, action, key and arguments throughout recovery.

Use the fixed UUIDv7 timestamp policy from ADR-1670: timestamp plus 24 hours, at
most five minutes ahead of database time. Mirror the existing central limits
under the SQL parity test; do not extend v2 in place. Serialize both actions on
the same subject/key advisory transaction lock and check wall-clock expiry before
and after lock acquisition, after conditional mutation and before return.

Check the receipt before inspecting the current note. Identical replay returns
the original stored snapshot. A changed action, target, expected version or patch
returns `PT409` / `idempotency_payload_conflict`. Otherwise, mutate only a row
matching both subject/ID and expected version. The existing database trigger
increments the version on update; delete returns the deleted row's version.
Stale, absent and RLS-hidden rows all return `PT409` /
`idempotency_version_conflict`, without revealing existence. Mutation and success
receipt commit together. Failures roll back and do not reserve a key or store a
negative receipt. Delete cascades also roll back if receipt insertion fails.

Receipts retain the original post-update row or deleted row. Replaying an update
after a later edit/deletion does not reapply it. Replaying a delete does not
execute another deletion. An expired writer always refuses the key, even if its
receipt has been purged. These are database guarantees under the same clock,
history and owner-authority assumptions as ADR-1670, not external-effect or
disaster-recovery guarantees.

Add `read_note_update_receipt_v2` and `read_note_delete_receipt_v2` with matching
arguments. Both are approved `STABLE SECURITY INVOKER` lookups, run in READ ONLY
transactions and take no advisory locks. Compare the complete canonical request.
A retained expired receipt may still confirm its original snapshot. Missing
unexpired receipts return zero rows; missing expired receipts return `PT410` /
`idempotency_key_expired`. Empty results and expiry never establish whether an
earlier uncertain request committed.

Use scoped SELECT and INSERT-only receipt RLS, restore the transaction-local RPC
scope on success/error, and revoke public function execution. There is no
application UPDATE/DELETE receipt policy. Reuse ADR-1670's maintenance trigger to
reject receipt updates and premature owner deletion. Add owner cleanup selection
`--contract v2 --receipt-kind mutation`, preserving default creation cleanup and
legacy reports. Mutation cleanup uses generated key deadlines, bounded batches,
row `SKIP LOCKED` and the shared mutation advisory lock. It never deletes notes.

Export `classifyVersionedMutation` and `VersionedMutationResult` from the Data
SDK. Classify an already completed write response with a synchronous application
predicate binding its snapshot to the original operation. Return `applied` only
for one matching row; recognize `version_conflict` only for the precise HTTP
409/SQLSTATE/message contract. Preserve existing `payload_conflict` and unresolved
receipt/expiry outcomes through `reconcileMutation`. Preserve selected types and
original errors, execute no query and never retry or replace an operation key.
Use `reconcileMutation` for separate read-only lookups. A conflict from a latest
attempt does not itself confirm an earlier uncertain operation.

Expose typed starter methods `updateOnceV2`, `removeOnceV2`,
`readUpdateReceiptV2`, and `readDeleteReceiptV2`. These are explicit opt-in methods;
the existing update/delete methods and browser editor keep their prior contract.
Apply the owner migration/RPC-permission workflow, refresh the runtime, regenerate
types and install the paired SDK before adoption. No platform endpoint or new
connection pool is introduced.

## Consequences

Applications can recover a successful conditional update or delete after its
response is lost, even after the business row has changed or disappeared. They
must retain their original operation context and distinguish original receipts
from current row state. A new key represents a new operation; stale versions
require explicit application review rather than automatic key/version changes.
Live receipts remain protected for the fixed replay window, and owners choose
when to purge them after it closes. Ordinary REST writes remain available under
their existing semantics and do not acquire receipts automatically.

## Validation

SDK unit/inference tests verify exact conflict contracts, selected snapshot types,
expiry uncertainty and no automatic request execution/retry. Packed starter
client tests verify typed RPC routing and complete recovery arguments. Native
PostgreSQL/PostgREST acceptance exercises same-key concurrency, update/delete
races with same and different keys, stale/absent/hidden rows, lost responses,
uncommitted work, snapshots after later edits/deletion, receipt-insertion rollback
including delete cascades, patch validation, read-only scopes, direct receipt
denial, protected cleanup, expiry while waiting on operation/business locks, and
purge followed by late retries. Existing creation and browser journeys remain
regression coverage.
