# ADR 1051: Consumer-group batch settlement

Status: accepted for preview implementation; runtime validation pending.

## Decision

Add POST group `claims/settle/batch` on account-owned and scoped-worker routes.
Accept `settlements`, 1..32 ordinary complete/retry/renew requests, with a 16 KiB
body bound. Validate every item and reject duplicate numeric sequences (including
alternate representations of the same number) before mutation. Results preserve
request order and echo sequence/action alongside the ordinary settlement fields.

Lock the group once and process each item using the existing settlement operation.
Verify live lease, claim token and scoped credential identity separately for every
item. A missing, expired, settled or differently owned claim yields `lease_lost`;
it does not block valid items. All valid operations and known lease failures commit
together. Malformed requests, denied permissions or unexpected database errors
abort the whole transaction. The batch is not a replay receipt: an ambiguous response
may have committed, and resubmission can return `lease_lost` for a completed item.
No new exactly-once guarantee is made.

Account routes retain ownership, MFA, deploy-write and preview checks. Scoped
routes authenticate before parsing, load the group using the first action and
require every distinct action in the fresh credential permissions under the group
lock. Existing group-to-credential lock order stabilizes revocation/rotation and
expiry checks. No new worker permission or migration. A batch records one credential
transaction use (first action), while every HTTP request retains authentication and
request budgets. Each item still checks lease expiry at operation time.

Reuse the ordinary settlement engine for completion metrics, retry policy,
dead-letter webhook triggers, renewals and deadline clipping. Successful complete
items each increment the lifetime completed counter. Batch settlement does not
advance claim priority turns or spend claim-rate credit. Pause/drain allow settlement
as before. Use sqlc for shared single/batch lease verification and fresh action checks.

Generated Node/Python SDKs expose both routes; the isolated Node worker client gains
`settleBatch` without changing global authentication. Node processing helpers opt
in via `settlementBatchSize` (1..32, default 1), with `settlementFlushMs` (0..25,
default 10). Batch only complete/retry; renewals bypass the queue. Flush at size or
timer, track each handler until its settlement result, and keep renewals/deadline
observation active while opted-in settlement waits. Single-settlement defaults keep
the previous renewal-stop behavior. Processing durations include batch queue time.

Queued cancellation removes unsent entries. Shutdown cancels in-flight requests;
server commits may still have happened. Validate ordered result correspondence and
statuses before resolving individual items. Lost lease or transport/protocol failure
stops the helper; it never automatically retries ambiguous settlements. Other items
in the same batch may already be committed. Concurrent renewal and settlement can
race, so cancellation is not proof of server rollback. Handlers still need idempotent
side effects and fencing for external writes. Existing concurrency bounds pending
and in-flight settlements; batching with one handler may yield one-item requests.

Go/Node builds and static specification/generation checks provide compile evidence
only. Runtime lease/permission/transaction/cancellation and database execution checks
remain deferred at the user's request.
