# ADR 1055: Consumer-group settlement receipts

Status: accepted for preview implementation; migration/runtime validation pending.

## Decision

Accept an optional UUID idempotency_key on single settlement and on the whole batch.
Existing requests without keys retain existing behavior. Item-level batch keys are
invalid. Receipt identity is group UUID, principal (account or scoped credential UUID),
and key. Single and batch share that namespace; their request kinds are fingerprinted.

Hash validated request content with canonical decimal sequences and claim-token
hashes. Preserve action, retry-delay omission versus explicit zero, safe failure code
and batch order. Store only the final SHA-256 fingerprint and response JSON, never
raw request content, claim tokens or credential tokens. Reuse with changed content
returns 409 realtime_settlement_key_conflict before any mutation. A different principal
has a distinct key namespace and cannot retrieve another principal's receipt.

Perform lookup under the existing group transaction lock, after authorization and,
for batches, fresh checks of every requested action. Revoked, expired or stale worker
credentials cannot read receipts. Account requests retain ownership, app scope and MFA
checks. Rotated credentials can replay with a currently valid token for the same
credential identity and action permissions. Receipt possession grants no authority.

Return a matching saved response before rechecking the original lease: completion
may already have deleted the snapshot, or release/retry may have been claimed again.
A replay never mutates the message or repeats metrics, history, circuit transitions
or retry delays. Worker credential use accounting still observes each request.
Receipt replays describe the original outcome, not the current message/lease state.
Manual renew is supported, but replays return the original expiry without extending
again; each new renewal requires a new key.

Save settlement and response in the same transaction, including per-item lease_lost
batch outcomes. Database failures roll back both. Single lease-loss/conflict failures
produce no receipt. The same key can retry after a failed transaction. Commit-response
loss is resolved by reading the committed receipt on retry. No handler side-effect
or external exactly-once guarantee is introduced.

## Bounds and retention

Retain receipts for 24 hours from their original commit path; replay does not extend
retention. Cap at 1115 receipts per group shared by all principals and request kinds.
Prune expired rows on keyed requests. Check capacity before settlement and return
429 using the existing typed capacity error; never evict live receipts to admit new
keys. Existing receipt replay works at capacity. Requests without keys consume no
receipt storage. Idle expired storage stays physically bounded until the next keyed
request. Group deletion cascades receipts; resync and credential deletion retain them
until expiry. After expiry the key has no replay protection: callers must not depend
on it beyond the retention window. New stale single tokens fail; batches can record
new lease_lost outcomes after the old receipt expires.

The SQLC projection and generated queries own all new runtime SQL. The migration
bounds stored response size to 16 KiB as a backstop; no plaintext tokens enter it.

## SDK

Node worker processing adds settlementRetries, an opt-in integer 0–3 (default 0).
Nonzero generates one UUID per actual single/batch terminal settlement, copies the
request once, and reuses it for every attempt. Batch retries preserve all original
items and their order; they never retry only selected items. Renewals remain immediate
and are not automatically retried. Release uses receipt retries within the existing
five-second shutdown request budget.

Retry only fetch TypeError and HTTP 408/500/502/503/504, with 100/200/400 ms delays.
Cancellation cancels the current request or pending delay and prevents new attempts.
Auth, validation, lease/key conflicts and 429 receipt capacity errors do not retry.
Handler callbacks run once per processed claim; retries only repeat settlement.
Node and Python generated APIs accept keys for callers managing retry policies and
persisting keys themselves.

## Rollout and verification

Apply migration 20261012102700527 before the new binary, after cooperative release.
Roll out binaries before enabling SDK retries; older binaries do not provide receipt
protection. Downgrade refuses live receipts until they expire, then drops the table.
No receipt inspector, extra webhook or permanent receipt archive in this change.

Go/Node builds, SDK generation, Python AST, OpenAPI parity, SQLC regeneration parity
and whitespace checks only. Runtime tests and migration execution are deferred by
request.
