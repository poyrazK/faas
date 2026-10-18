# Durable Python acknowledgment intents

[`run_durable_with_processing_lease`](event-consumer-processing-python.md#compose-with-acknowledgment-workers)
also commits a matching unexpired local claim's removal in the business/receipt/
intent transaction. Stop/join local renewal first. Receipt-only reconciliation
still skips local lease checks and never repeats preparation.

For preparation with coordinated local/platform renewal, use
[`run_prepared_durable`](event-acknowledgment-python-preparation.md). It keeps the
same atomic completion and saved-callback recovery contract.

`AsyncEventAcknowledgmentWorker.run_durable` commits business SQL, the consumer
receipt and a completion intent in one application PostgreSQL transaction. A
separate recovery host sends the saved callback. A crash after local COMMIT does
not require running business code again. This uses the same application-owned
[intent protocol as Go](event-acknowledgment-intents.md) and
[Node](event-acknowledgment-node-intents.md).

## Install and protect

Explicitly install `event_consumer_receipt_schema` and
`event_acknowledgment_intent_schema` in the business database. The latter is packaged
byte for byte from the Go SDK schema by `scripts/gen-operation-inbox-schema.py`.
No helper installs, migrates or prunes either table. Existing immediate worker
methods continue to use only the receipt table.

Apply `event_acknowledgment_intent_stored_retry_policy_schema` once in an
application migration transaction before upgrading existing intent workers.
Durable staging accepts `retry_policy` to bind new intents atomically; recovery
captures and honors bindings across all three SDKs. See
[upgrade, staging and rollout](event-acknowledgment-stored-retry-policy.md).

For existing unbound backlog, use
`aadopt_event_acknowledgment_intent_retry_policy` with frozen options holding a
saved command UUID and the selected timestamp/count. Install the additional
optional receipt schema first. Adoption preserves callback data, counts and due
times, and resolves original retries with retained receipts. See
[policy adoption and cancellation](event-acknowledgment-policy-adoption.md#python-adoption-and-replay).

Current Python helpers also require `event_acknowledgment_intent_retry_budget_schema`
after the v1 upgrade, even for unbound/v1-only work. Optional `retry_budget` binds a
finite total of committed recovery claims. Exhaustion parks protected callbacks;
an explicit guarded command grants a larger total and retains an idempotent
receipt. See [budget installation, parking and resume](event-acknowledgment-retry-budgets.md#python-budgets-and-guarded-resume).

Pending/unconfirmed/parked intents contain the raw platform token and completion JSON.
Restrict database/backup/worker access and protect their storage. Status objects
contain metadata only. Claims redact string/repr and reject copying/pickling;
they are process-local capabilities, never a persistence format. Python private
attributes and debugger locals are not a security boundary. Do not publish them.
Intent storage errors use fixed messages/codes and omit driver diagnostics, which
may contain rejected rows. Application SQL/business errors remain application-owned.

Use the configured generated `AuthenticatedClient` (`FaaSClient.inner`), exclusive
idle autocommit psycopg async connections, and bounded pool acquisition, driver and
PostgreSQL statement timeouts. Authenticate/authorize ingress first, durably retain
the original bytes/headers before HTTP 202, and retain exclusive ingestion-job
ownership. Keep one mode per attempt: immediate and durable callbacks must not mix.

## Commit completion atomically

```python
async def apply(cursor, cancel):
    cancel.check()
    await cursor.execute("UPDATE invoices SET paid=true WHERE id=%s", (invoice_id,))
    cancel.check()
    return b'{"applied":true}'

queued = await worker.run_durable(connection, apply, stop_event=job.stop_event)
# queued.receipt_confirmed is True; queued.intent is locally committed.
# Recovery now owns callbacks. job.ack() must independently fence ownership.
await job.ack()
```

The handler uses only its supplied cursor and transactional SQL. External effects
need an outbox or an independent idempotency protocol. A private pre-commit hook
saves the intent for both new and replayed receipts. Hook failure, intent conflict
or pre-commit cancellation rolls back the whole transaction, including new
business writes. Saved completion JSON is never reserialized.

Without a receipt, the worker confirms the platform lease and renews during
processing using the [existing cancellation contract](event-acknowledgment-python-worker.md).
Renewal stops and joins before return. Durable staging sends no terminal callback.
Run recovery promptly: staging does not extend the platform lease after processing,
and HTTP 202 does not keep a VM awake. An expired attempt can require redelivery;
the receipt still prevents repeated committed effects for that consumer/acceptance.

`queued.intent` is a snapshot from staging; recovery may already have advanced it.
Local commit confirms durable callback responsibility, not platform completion.

## Reconcile uncertain commits

```python
queued = await worker.save_completion_intent(fresh_connection, stop_event=job.stop_event)
```

This receipt-only path never runs business code or renews the platform lease.
Use it after `EventConsumerCommitUnknownError`. Missing receipts raise
`event_consumer_receipt_missing` and require an explicit owner policy. Absence
alone is not proof of rollback or exclusive ownership.

Unknown receipt/intent COMMIT returns no confirmed intent result, preserves
`EventConsumerCommitUnknownError` and closes the exclusively leased connection.
Discard it through the pool and acquire a fresh idle autocommit connection. Never
infer rollback or choose a negative acknowledgment from uncertainty. Retrying
`run_durable` with the same original delivery and exclusive ownership waits on
the receipt identity lock: a receipt skips the handler, while a missing receipt
still requires confirmed platform renewal before new work.

Repeated staging binds owner scope, invocation, generation/attempt, consumer/input
digests, app slug, advertised lease, token digest and exact action/JSON digest.
An identical stage retains its state, claim, counters and due time. Changed
immutable inputs conflict. Newer generation/attempt pairs supersede unfinished
older intents, clear their secrets and invalidate claims. Stale stages cannot
replace newer ones; confirmed history remains confirmed.

## Persist explicit failure decisions

After the owner establishes known rollback, stopped work and exclusive durable-job
ownership, persist the selected action/reason with that job, then stage it:

```python
intent = await worker.save_retry_intent(
    connection, "dependency_unavailable", stop_event=job.stop_event,
)
# Or worker.save_dead_letter_intent(connection, "invalid_business_input", ...).
# Confirmed local intent COMMIT transfers callback responsibility to recovery.
```

These methods send no HTTP callback or platform renewal and invoke no business
handler. They return committed `EventAcknowledgmentIntentStatus` metadata. Reasons
must match `[a-z0-9_.-]{1,128}`; use stable codes, never exception text or secrets.
No exception automatically selects a failure action. Unknown business COMMIT is
an explicit receipt-only reconciliation case, not permission to reject delivery.

Staging holds the same advisory transaction lock as receipt processing, checks
receipt owner/consumer/acceptance/source/event and request digest, then saves the
intent under the existing invocation lock in that transaction. A matching receipt
blocks rejection with `event_consumer_receipt_committed` and
`error.result.receipt_confirmed=True`. The probe does not load the saved result;
`error.result.receipt` is `None`. Use receipt-only completion. Mismatched receipt
scope/input raises `EventConsumerConflictError` without changing either table.

An absent receipt is only an observation; the application must establish rollback
and stopped work independently. Receipt/intent locks do not grant durable-job
ownership or stop unrelated external effects. Keep modes and owners consistent.
Existing immediate `retry`/`dead_letter` retain their original callback behavior.

Repeat uncertain saves with the same original delivery and chosen action/reason.
The shared action digest includes both; changing either conflicts, while identical
saves retain claims, counters, due times and confirmation. Newer attempts use the
existing supersession/secret-erasure rules. Unknown negative-intent COMMIT returns
no confirmed status, closes the connection and preserves
`EventConsumerCommitUnknownError`; reconcile on a fresh idle autocommit connection.
Cancellation shields active SQL/cleanup, checks before commit, and preserves a
known successful COMMIT even if cancellation arrives late.

Recovery handles Python/Go/Node negative intents and checks for a receipt again
before sending one. A present receipt supersedes the intent with
`receipt_committed`. This check does not fence unrelated legacy processing.
See the [persisted failure-decision example](../examples/event-acknowledgment-python/README.md#persist-a-failure-decision).

## Recover one saved callback

For finite catch-up with controlled concurrency, use the separate
[bounded recovery pass](event-acknowledgment-python-recovery-pass.md). It composes
this same primitive with exclusive per-iteration connection scopes and frozen
partial progress; the host still chooses when another pass runs.

```python
from faas_sdk import EventAcknowledgmentIntentError, arecover_next_event_acknowledgment_intent

try:
    status = await arecover_next_event_acknowledgment_intent(
        client.inner, connection, account_id, app_id,
        claim_duration_ms=30000, backoff_ms=1000, stop_event=shutdown,
    )
    # Inspect state/last_code; a successful return need not mean confirmation.
except EventAcknowledgmentIntentError as error:
    if error.code != "event_acknowledgment_intent_empty":
        raise
    # No due unclaimed row. The application chooses when to poll again.
```

For separate steps, use `aclaim_event_acknowledgment_intent(connection, account_id,
app_id, ...)`, then `claim.deliver(client.inner, connection, ...)`. Both accept
`stop_event` and `callback_options=EventAcknowledgmentWorkerOptions(...)`.
Acquisition accepts `claim_duration_ms` (whole milliseconds 1000..300000; default
30000). Delivery accepts `backoff_ms` (native integer milliseconds 1..3600000;
omitted/None selects a fixed 1000 ms delay without a retry policy).
The combined helper accepts both. Callback budgets use the live worker's
lease-dependent bounds and are checked against the row before its claim commits.

Delivery, single-iteration recovery and the bounded pass also accept
`retry_policy=EventAcknowledgmentIntentRetryPolicy(...)` for capped exponential
full jitter using persisted claim counts. Leave `backoff_ms` omitted/None with a
policy. The shared schema and callback fences are unchanged. See
[adaptive retry scheduling](event-acknowledgment-python-retry-policy.md).

Claims use owner scope, database time, 32 random bytes and `FOR UPDATE SKIP LOCKED`.
Each claim increments the saturating delivery-attempt counter. Recovery verifies
saved capability/action hashes, preserves full int64 generations using Python
integers, and sends the original token and exact result/reason. It never executes
business handlers or renews platform leases.

Remaining database claim time minus all elapsed local time bounds HTTP/retries.
Settlement rechecks the opaque claim token, unfinished state and database expiry.
Expired, replaced or superseded claims cannot settle another worker's row. There
is no PostgreSQL/HTTP transaction: an in-flight callback may finish after local
supersession. The platform attempt/token fence protects newer platform attempts.
A successful callback with uncertain settlement can be repeated with unchanged
inputs and server idempotency.

| Local state | Meaning |
| --- | --- |
| `pending` | Saved callback is due or claimed for recovery. |
| `unconfirmed` | Uncertainty retains protected inputs, releases the claim and schedules database time plus backoff. |
| `confirmed` | Matching callback succeeded and local settlement committed. |
| `superseded` | Newer lineage, unavailable platform attempt or a receipt blocks this intent. |

Terminal settlement clears raw tokens, results and claims. In-memory capabilities
and backups can still contain secrets. Thrown storage, cancellation or claim-loss
errors can leave active claims until expiry; they do not establish callback
failure or settlement. Unknown settlement COMMIT closes the connection and retains
the unknown outcome category. Inspect metadata using a fresh connection.

Public operations shield active SQL/cleanup from native task cancellation, signal
cooperative control and join owned work. HTTP is cancelled/joined when the callback
budget or claim deadline ends. Driver cleanup still requires bounded driver
timeouts; cancellation is not a hard transaction timeout. Claims belong to one
event loop and reject overlapping delivery calls. Expiry may permit another
process to retry while a slow transport drains; unchanged-input idempotency remains
necessary at the server.

## Diagnostics and retention

`aget_event_acknowledgment_intent_health(connection, account_id, app_id, options,
stop_event=...)` returns a bounded read-only summary of unfinished callback intents:
due/leased/scheduled/stalled counts, expired claims, oldest ages and explicit
coverage/completeness. It uses one database snapshot/clock and exposes no capability
or payload. Always export `complete`/`truncated` with its metrics. See the
[Python recovery health contract](event-acknowledgment-intent-health.md#python-summary).

`awatch_event_acknowledgment_intent_health(connection_factory, account_id, app_id,
options, notify, stop_event=...)` adds sustained due/stalled alerts from fresh,
complete observations. Each read leases a connection through an application-owned
async scope factory. Reads/notifications run sequentially with cooperative cleanup;
missing evidence never resolves a condition. See the
[Python watch contract](event-acknowledgment-intent-health.md#python-watch).

For stable incident IDs and pending alert replay across restart, use the separate
[checkpointed Python watch](event-acknowledgment-python-checkpoints.md). Its pure
validation/acknowledgment helpers and fenced application-owned persistence keep
notification state separate from callback delivery intents and business receipts.

`alist_event_acknowledgment_intents(connection, account_id, app_id, invocation_id=None,
limit=25, stop_event=None)` returns immutable metadata, at most 100 rows. It does
not claim work or return raw results/tokens. Generations/counters are native
integers; timestamps are UTC RFC 3339 strings with microseconds.

The application owns scheduling, concurrency policy, retention and host/client lifetime.
Keep receipts through the retry/replay horizon and intent metadata while stale
jobs/restores can return. Deleting the newest intent removes the supersession
fence. No pruning, platform migration, route, global ordering or external
exactly-once guarantee is added. Consumer deliveries remain independent.

See the [versioned durable integration](../examples/event-acknowledgment-python/README.md#durable-callback-mode).
Local evidence covers generation, packaging, compilation, lint and static checks.
Hosted PostgreSQL/callback/restart/cancellation and cross-SDK qualification remains
required before release; no local tests or assertion changes are included.

For existing active unbound/v1 backlog, use the separate
`aadopt_event_acknowledgment_intent_retry_budget` command. Install its optional
receipt schema, select precise metadata, authorize and durably save original
options, then apply timestamp/count/policy guards. It rejects live claims/newer
attempts and preserves v1 timing, callbacks, counts and due times. See
[Python budget adoption and replay](event-acknowledgment-budget-adoption.md#python-selection-adoption-and-replay).
