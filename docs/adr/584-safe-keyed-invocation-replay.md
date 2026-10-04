# ADR-584: Safe recovery for failed keyed invocations

- **Status:** accepted
- **Date:** 2026-10-05
- **Decision:** Recover failed unbound keyed invocations through a dedicated,
  account-scoped replay operation that derives work identity from the original
  execution and joins its existing lane. Block generic replay from discarding
  keyed or queue delivery identity.
- **Why:** Generic replay creates an HTTP invocation without a work binding.
  Replaying a failed keyed event consumer through it could bypass the per-key
  claim fence, FIFO admission order and captured fairness cap.
- **Consequences:** Recovery remains independent per consumer and observable
  through ADR-582 receipts and ADR-583 replay lineage. Operators must publish
  new work when its original pending lifetime has expired.

## Admission and ordering

`POST /v1/invocations/{id}/replay-keyed` requires the same deployment write
scope, configured MFA and account rate limit as generic replay. Durable parent
identity provides idempotency regardless of the `Idempotency-Key` header; the
route does not serve an HTTP response cache before ownership checks.
The original execution and its current app must still belong
to the caller. Only `failed` keyed work without a queue binding or named queue
is eligible. Completed, active, cancelled, superseded, expired and dead-lettered
work is rejected. Existing in-place queue dead-letter replay remains available.
Customer self-service generic replay also rejects policy/binding work; an
account operator uses the appropriate durable recovery surface.

PostgreSQL locks the existing lane before the original execution and app.
It allocates the lane's next sequence and creates a new invocation with the
captured policy name/revision, key digest, fairness digest/cap, environment,
customer identity, request, retry policy and failure rules. It does not resolve
the current work policy or recompute keys from headers or payload. Existing
deployment pins are checked before a new child is admitted and rechecked by
the ordinary dispatcher. The ledger derives trusted parent/root lineage.

The replay follows previously admitted work in that lane, including broker
records. It does not supersede newer pending work or restart debounce. Normal
future `keep_latest` admissions may supersede a pending replay. Its original
`work_expires_at` and `start_deadline_at` remain effective; an elapsed pending
deadline rejects new recovery with `keyed_replay_expired`. A blocked replay can
therefore expire before it reaches the head. The API refreshes the execution
deadline/result lifetime using the caller's plan, as generic replay does.
There is no global ordering or exactly-once external side-effect guarantee.
For events, a fresh lifetime requires a new event ID; retrying the same accepted
event retains its original work identity and deadline.

## Durable duplicate behavior and retention

`invocation_keyed_replays` records one child per original failed execution in
the same admission transaction. Concurrent and repeated requests return that
child, including after success, failure or deadline expiry. A subsequent
recovery targets the failed child. This prevents branching from an older failed
execution and does not rely on the HTTP idempotency cache's lifetime.

The marker references its parent with cascading deletion and has no child
foreign key. Pruning a child while retaining its parent leaves the marker:
the parent returns `keyed_replay_unavailable`, rather than creating another
execution. Pruning the parent deletes its marker; surviving descendants keep
their trusted root identity and can recover from their own failed execution.
Duplicate reads precede deployment pin validation, so expiration of an old pin
does not turn an already accepted replay into a fresh request or erase its ID.
MemStore mirrors these transitions under its mutex.

## Receipts and CLI

Receipts expose `keyed_handler_replay` only for an owned failed keyed execution
with an unexpired pending lifetime and no previously created child. Latest
retained replay outcomes use the same rule, so pruning cannot suggest unsafe
re-execution of an older parent. The original failure remains visible alongside
the latest child and retained replay history. Other consumers remain untouched.

`gregale invocations get --replay-keyed ID` uses the dedicated route. It is
mutually exclusive with `--replay`. The Go client exposes
`ReplayKeyedInvocation`; OpenAPI documents admission, ordering, expiry and
duplicate behavior. Event inspection prints the selective recovery request.

## Qualification

Acceptance covers concurrent replay requests, tail ordering without pending
replacement, active same-key claims, captured fairness, subsequent recovery,
trusted lineage, stale completion, scope rejection, elapsed pending deadlines,
and pruning without duplicate execution. Mixed-consumer API coverage retains
one consumer's success while recovering another and suppresses unavailable
recovery actions. PostgreSQL exercises the actual lane lock and ledger write.
Portable API/client/CLI tests, generated SQL parity and scoped lint are required.
Full Linux CI and staging delivery qualification remain release gates.

Local verification used Go 1.25.13 and PostgreSQL 16.15. The full portable state
suite and PostgreSQL keyed replay cases passed, including concurrent broker
claims, pruned parents/children and reused child IDs. Receipt, keyed claim and
fairness PostgreSQL regressions passed. Focused API tests passed for mixed
consumers, customer identity/suspension, write scope, MFA and expired deployment
pins. Client and CLI recovery/JSON/command-reference checks passed; generated
SQL matched regeneration and the changed packages had zero lint issues with
golangci-lint 2.4.0. Scheduler event and keyed-work regressions also passed.

Initial builds ran out of disk space, and a temporary build volume disappeared.
Verification moved to persistent task storage. Full API and CLI runs encountered
disk failures in unrelated fixtures; all fourteen failed API cases and four
failed CLI cases passed on rerun after space became available. These runs do
not replace the full Linux CI and staging gates.
