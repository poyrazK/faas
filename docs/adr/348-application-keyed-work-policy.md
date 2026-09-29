# ADR-348 · Application-keyed background work policies

- **Status:** accepted; invocation, event, and unnamed queue producers implemented; trigger adapters pending
- **Date:** 2026-09-28
- **Decision:** Define one named, app-scoped work policy for durable invocation
  producers. Producers resolve an application key at admission and persist the
  policy name, key digest, policy revision, and effective deadlines on each
  invocation. A database-backed claim gate enforces per-key concurrency and
  FIFO order across scheduler replicas. Pending replacement, debounce, expiry,
  and fairness are modes of this same policy rather than separate products.
- **Why:** The current account cap, queue binding concurrency, and per-app
  drain ordering cannot coordinate work for an application-defined identity
  such as an order, document, or customer tenant.
- **Consequences:** Enqueue, claim, retry, completion, cancellation, and lease
  recovery must share the same durable policy state. Broker trigger records and
  independent job tasks need adapters before they can claim this capability.
- **Rejected alternatives:** A Go mutex cannot coordinate scheduler replicas.
  A queue per key is unbounded and exposes transport mechanics to customers.
  Advisory scheduling without a claim fence cannot reject a stale worker's
  completion after lease recovery.

## Contract

A policy is named within one app. Its lane identity is `(account_id, app_id,
policy_name, key_digest)`. The policy name is stable across producers, so an
event subscription and an explicit invocation can coordinate on the same lane.
Each producer supplies a bounded scalar key or a validated, scalar-only path
into its payload. A missing, null, object, or array key fails admission. The
resolved key is digested before persistence and never used as a metric label.

The policy has these independent controls:

| Control | Meaning |
|---|---|
| `max_running_per_key` | Concurrent claims within one lane; v1 starts at one. |
| `pending_updates` | `all` retains every pending row; `keep_latest` supersedes only pending rows in the lane. |
| `debounce` | A new accepted row becomes eligible after a quiet period measured from server admission time. |
| `expires_after` | Pending work becomes `expired` if it has not begun by the deadline. Expiry differs from execution timeout, retry exhaustion, and result retention. |
| `fairness_key` and its running cap | Optionally bound active claims for one application customer across many work keys. A saturated group is omitted from due-work scans. |

For `pending_updates: all`, an older pending retry blocks later rows in the
same lane until it succeeds, expires, or becomes terminal. This is FIFO by
admission order, not by a mutable `due_at`. `keep_latest` means newest *accepted
arrival*, not greatest application version. Customers needing version order
must supply a monotonic version or check the authoritative version in their
handler. A newly accepted row cannot replace one already dispatching.

An event may request `cancel_pending` for a policy/key. It affects only pending
rows and returns the number cancelled. A running worker may still finish. An
optional version watermark is required if an old producer retry could enqueue
work after the completion event. Cancellation by key without such a watermark
only describes work present at the instant of the transaction.

Event fanout snapshots the subscription's work binding when the event is
accepted. A later selector or action change cannot redirect that accepted
event. Receipts created before binding snapshots retain live-binding lookup
for backward compatibility. The effective policy revision is still recorded
when the invocation enters the work ledger.

## Durable transitions

1. `apid` remains the owner of policy configuration. Strict manifest/API
   validation produces a named policy revision. Each admitted invocation
   snapshots the effective policy revision and resolved key digests, while the
   stable policy name keeps a lane shared across revisions.
2. Enqueue checks request/event idempotency **before** replacement. It locks the
   lane, marks eligible pending rows `superseded` when configured, then inserts
   the new row and its `available_at`/`expires_at` in one transaction. A replay
   of the same event must not supersede newer work.
3. Claim locks the same lane, checks the lane and optional fairness cap, then
   atomically reserves the existing account async quota and changes the chosen
   row to `dispatching`. Every claim mints a token or increments a generation.
   Completion, retry, and failure require that claim identity. A stale receipt
   is rejected without changing a newer claim or releasing its capacity.
4. Terminal, retry, and expired-lease transitions release all reservations in
   the same transaction. Reconciliation repairs leaked reservations after a
   process crash. A retry remains the oldest nonterminal row in its lane.
5. The scheduler omits saturated fairness groups from due-work scans so a
   large backlog does not keep pinning every due page. Full round-robin
   selection among unsaturated groups is a later scheduler improvement.

The claim token fences Gregale's ledger. It does not revoke a worker's access
to an external database after ownership loss. External side effects still need
an application idempotency key, version predicate, or an external system that
honors a fencing token. Delivery remains at least once.

## Delivery sequence

1. Add the shared policy value model and validation, then additive schema and
   state-store conformance cases. Existing unkeyed invocations behave exactly
   as before.
2. Add keyed enqueue and claim for the `invocations` ledger. Wire internal
   event subscriptions, explicit async invocations, and delayed tasks through
   that path. Keep the policy internal until the full claim and recovery path
   passes PostgreSQL contention tests.
3. Add strict manifest, OpenAPI, CLI, generated SDK, and inspection surfaces.
   Publish the capability as `internal` until cross-component acceptance is
   complete.
4. Adapt queue-trigger and external broker `trigger_records` dispatch. Broker
   acknowledgement, partial batches, and redelivery must preserve the keyed
   claim and replacement contract. Adapt independently materialized jobs only
   after their separate lease and cancellation semantics are reconciled.

Acceptance must cover concurrent schedulers, duplicate event replay,
replacement versus claim, retry ordering, lease expiry and stale completion,
cancel-versus-claim, policy revision changes, bounded key cardinality, and a
large single-key backlog that cannot starve another fairness group.

## Queue and broker adapter boundary

Policy-tagged delayed tasks use the invocation drain, including when a queue
trigger is bound to delayed tasks. The queue poller excludes these rows, so it
cannot bypass the keyed claim gate. These tasks are delivered individually.

Unnamed queue-send and application-inbox messages now enter the keyed
invocation ledger and use its claim gate. The API rejects a work policy when
a named queue is requested or an active queue consumer selects a named queue.
Those messages retain their existing trigger and batch contracts. An active
consumer added after a keyed message is admitted does not take over that row;
the invocation drain continues to own it.

Named queue-trigger messages and external broker records need a shared
work-item claim ledger before policy fields can be exposed on those producers.
The trigger path now persists a claim generation and a ten-minute lease on
`trigger_records`; retry, completion, and dead-letter transitions for broker
records reject an expired or superseded claim. Queue polling can recover an
expired record claim, and the scheduler claims only records present in its
polled broker batch. These are claim safety prerequisites, not a work-key
reservation: broker handles are still acknowledged outside the invocation
transaction. The adapter must resolve keys at durable record
admission, reserve lane and fairness slots across both ledgers, carry a claim
generation through the gateway result, and release reservations on retry,
terminal outcome, and lease recovery. A late broker acknowledgement must not
finish a newer claim. Replacing a pending broker record also needs a durable
terminal disposition before acknowledging its source handle. Adding selector
fields to a trigger alone would not enforce these transitions.
