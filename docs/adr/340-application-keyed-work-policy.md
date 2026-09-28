# ADR-340 · Application-keyed background work policies

- **Status:** proposed
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
| `fairness_key` and its running cap | Optionally bound work for one application customer across many work keys; ready fairness groups are selected round-robin. |

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
5. The scheduler selects ready lanes fairly before choosing a row. Sorting one
   bounded due-at page in Go is insufficient: a large backlog can occupy the
   whole page before other lanes are seen.

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
