# ADR-1672: Recoverable atomic note batches

Status: Accepted

## Context

Individual versioned mutations (ADR-1671) support replay and recovery, but several
SDK calls still commit independently. Applications need an explicit bounded
transaction for a group of note edits/deletions, including a single durable
acknowledgement when the response is lost.

## Decision

Add append-only starter migration `0014_note_atomic_batches.sql` and approved
invoker RPC `apply_note_batch_once_v2(uuid,jsonb)`. Accept an ordered array of
1–100 operations on distinct note IDs. Each item contains `operation` (`update`
or `delete`), positive PostgreSQL integer `note_id` and `expected_version`.
Updates require a nonempty `patch` containing only non-null string `body` and/or
PostgreSQL integer `priority`. Deletes forbid `patch`. Reject all other fields,
malformed items and repeated IDs with `PT400` / `idempotency_batch_invalid`.
The central limits table defines the operation cap and 1 MiB saved-result cap;
SQL/client parity tests enforce their published copies.

Derive subject from verified claims. Preserve all note, cascade and receipt RLS
checks. Serialize a batch with a subject/key advisory transaction lock in the
separate `gregale.note_batch.v2` namespace. Reuse the fixed UUIDv7 replay window,
five-minute future tolerance and clock/history/owner assumptions of ADR-1670.
Check expiry before and after the operation lock, after business row locking,
after each mutation and before acknowledgement. Expired writes always refuse
execution, including after receipt cleanup.

Look up the success receipt before consulting current notes. Store the complete
canonical ordered operation array; changing its order or any argument conflicts
with `PT409` / `idempotency_payload_conflict`. Matching replay returns original
snapshots without reapplying changes. For new work lock all matching subject/ID/
version rows in ascending ID order regardless of submitted order. Missing, stale
or hidden notes all return `PT409` / `idempotency_version_conflict`. Sorted locks
avoid cycles between these batches; customer triggers, cascades and unrelated
transactions can still cause ordinary lock errors.

Apply operations in submitted order after all preconditions match. Updates bump
the version once using the existing trigger; deletes retain the deleted version.
Append indexed action/snapshot results. If their JSON text exceeds 1,048,576
bytes return `PT413` / `idempotency_batch_result_too_large`. All operations,
cascades and the receipt commit together. Any error rolls back the entire batch
and stores no receipt or negative key reservation. PostgreSQL sequence values
and external effects from customer code are outside transactional rollback.

Keep immutable success receipts in `api.note_batch_receipts_v2`, keyed by subject
and request key, with generated key deadline and expiry index. Restore the
transaction-local RPC scope on success/error; expose SELECT only within writer/
reader scopes and INSERT only within writer scope. No application UPDATE/DELETE
policy. Reuse the immutable/early-deletion guard from ADR-1670. Revoke PUBLIC
execution and follow the existing owner RPC grant workflow.

Return exactly one `api.note_batch_result_v2` composite envelope with
`request_key`, `subject`, and ordered JSON `results`, including zero-based
`index`, `operation` and note snapshot. The existing standalone-composite type
generator and PostgREST support this without runtime changes. Nested JSON must
be validated before treating a response as a complete acknowledgement.

Expose `read_note_batch_receipt_v2` with identical arguments and validation as a
`STABLE SECURITY INVOKER` lookup. It runs in a READ ONLY transaction, takes no
advisory/row locks and performs no writes. Return the original envelope even if
expired; absent live receipts return zero rows, absent expired receipts return
`PT410` / `idempotency_key_expired`. Neither absence, expiry nor a conflict proves
whether an earlier uncertain operation committed.

Add typed starter methods `applyBatchOnceV2` and `readBatchReceiptV2`, both with
retries disabled. `prepareNoteBatch` validates and freezes one original subject,
key and ordered payload; it does not persist it. `noteBatchMatches` validates
every returned result against this pending operation before acknowledging it.
Add type-guard overloads to existing SDK `classifyVersionedMutation` and
`reconcileMutation`, preserving boolean predicates and all existing outcomes.
Classification executes no request and does not replace a key/version. Retain
the original API, identity, contract and payload throughout recovery.

Extend owner cleanup with `--contract v2 --receipt-kind batch`, using fixed SQL,
expiry bounds, row `SKIP LOCKED` and the same batch key advisory namespace.
Preserve default creation/mutation behavior. Cleanup never deletes notes and
cannot make an expired key executable again.

## Consequences

Applications can opt into one recoverable transaction for explicit note edits
and deletes. This does not add arbitrary SQL, filtered batch operations, atomic
SDK `bulkMutations`, or cross-request transactions. Ordinary REST, individual
mutation RPCs and browser editors retain their existing behavior. Adoption
requires owner migration, approved RPC grants, runtime refresh, regenerated
types and the paired SDK. Full operation arrays and original snapshots are
stored, so owners must budget storage and choose expired-receipt retention.

## Validation

Native PostgreSQL/PostgREST tests cover mixed ordered results, same-key replay,
independent subjects, opposite-order overlapping batches, stale/hidden/absent
notes, input limits and validation, lost responses, uncommitted work, read-only
scope restoration, direct receipt denial, later edits/deletion, RLS/receipt
failure with cascade rollback, result-cap rollback, live receipt guards,
locked cleanup, expiry during operation/business waits and late retries after
purge. Client tests reject partial/malformed acknowledgements and preserve
immutable pending inputs. SDK inference tests verify type-guard narrowing.
Starter/browser and CLI packaging checks cover integration and regression.
