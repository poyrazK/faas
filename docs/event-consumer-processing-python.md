# Python consumer processing leases

Python supports application-owned PostgreSQL leases for side-effect-free preparation
followed by a short business transaction. Each account, app, subscription and durable
acceptance has its own claim, so consumers recover independently. Schema,
identity/input digests, 32-byte tokens and database-clock fences match the
[Go protocol](event-consumer-processing.md) and
[Node implementation](event-consumer-processing-node.md).

## Install and claim

Explicitly install `event_consumer_receipt_schema` and
`event_consumer_processing_schema` in the application database. The latter is
packaged byte-for-byte from the canonical Go DDL. Helpers never install schemas,
start renewal loops, prune rows or schedule retries. Protect tables and backups:
the processing row contains an opaque claim token. Use exclusively leased idle
psycopg async connections with `autocommit=True` and bounded acquisition, query,
statement and cleanup calls. The SDK does not install a PostgreSQL driver.

After authenticating and authorizing ingress, preserve exact original bytes and
supply trusted owner configuration:

```python
from faas_sdk import aclaim_event_consumer_processing, event_consumer_request_from_headers

request = event_consumer_request_from_headers(headers, original_body, account_id, app_id)
async with application_pool.connection() as connection:
    lease = await aclaim_event_consumer_processing(
        connection, request, 30_000, stop_event=shutdown,
    )

prepared = None
if not lease.completed:
    # No business SQL or external effects; honor the owner's deadline/cancellation.
    prepared = await prepare_without_effects(original_body, shutdown)
    # Stop and join any local renewal loop before this final renewal/completion.
    async with application_pool.connection() as connection:
        await lease.renew(connection, 30_000, stop_event=shutdown)

async def apply(cursor, cancel):
    cancel.check()
    await cursor.execute("UPDATE invoices SET paid=true WHERE id=%s", (prepared.invoice_id,))
    cancel.check()
    return b'{"applied":true}'

async with application_pool.connection() as connection:
    receipt = await lease.complete(connection, apply, stop_event=shutdown)
# Return receipt.body unchanged only after confirmed completion.
```

Preparation may validate, read or calculate but must not perform external or
uncommitted business effects. For longer preparation, serialize renewals with a
safe margin and stop on any renewal failure or uncertainty. Stop and join local
renewal before final completion: its row lock prevents concurrent renewal from
extending the lease. Keep completion short enough to fit the remaining lease.

`duration_ms` is a required native integer from 1 through 9,223,372,036,854 inclusive,
the largest whole-millisecond Go duration. Booleans/fractional values are rejected.
This bound is separate from platform acknowledgment policy. Expiry uses database
time; a local timer or sleep cannot establish availability. Expired claims can be
replaced in place with a new token while their original request digest stays
immutable. An unexpired other claim reports busy and must not cause early success.

The capability belongs to one event loop, rejects copy/deepcopy/pickle and redacts
string/repr output. Keep private fields and protected application input out of
diagnostics. Reclaim from the same authenticated original request after restart;
never reconstruct it from a persisted token. The read-only `completed` property
is a snapshot meaning claim observed a matching receipt. It is not updated later
and proves no platform acknowledgment. When true, skip preparation and complete
to validate/replay the saved JSON. Renewing it normally reports lease loss because
no processing row is needed.

## Fence the short transaction

Completion acquires the existing consumer advisory lock and reads the receipt
first. A matching receipt replays after full scope/input/result validation without
a live local claim, including on an old capability. Without a receipt, completion
locks and validates token, input and expiry before invoking `handler(cursor, cancel)`.
Use only that supplied cursor and transactional SQL; do not control the transaction
inside the handler.

After business writes and receipt insertion, completion deletes only the same
still-unexpired claim. That conditional delete is the local completion
linearization point, matching Go/Node. A lost fence rolls back writes and receipt.
Expiry after the delete does not undo a later confirmed COMMIT. Optional completion
intent staging follows the delete in the same transaction; failure rolls back
writes, receipt, claim removal and intent together. Replacement claimants wait for
the transaction, then replay its receipt or take over after rollback/expiry.

An absent, deleted or mismatched receipt cannot authorize new business work through
an expired/replaced token. Receipt retention remains the business deduplication
boundary. Use leased processing consistently for a consumer; ordinary unleased
transaction and worker methods do not consult this optional table.

## Compose with acknowledgment workers

Construct `worker` from the same authenticated original headers/body and trusted
owner scope, following the [Python worker guide](event-acknowledgment-python-worker.md).
Install `event_acknowledgment_intent_schema` as well for durable mode:

```python
queued = await worker.run_durable_with_processing_lease(
    connection, lease, apply, stop_event=job.stop_event,
)
# Writes, receipt, processing-row deletion and completion intent committed together.
# queued is local commit evidence, not platform callback confirmation.
```

`run_with_processing_lease(connection, lease, apply, stop_event=...)` provides the
immediate alternative. Both check consumer/input digests before database/platform
calls and serialize operations on that capability through completion. Their initial
receipt-only probe never consults the local row or invokes business code. Existing
receipts skip the handler and local fence; durable mode stages the saved result,
while immediate mode completes it.

For new transactional work, the worker confirms the platform lease and maintains
its existing renewal monitor through the transaction, then joins it before
returning. Local and platform leases protect separate boundaries: local renewal
does not renew the platform claim, and neither worker method renews leases during
application preparation outside its call. Bound preparation to the remaining
platform budget or explicitly maintain its platform lease through the existing
trusted ownership protocol. An expired attempt must fail platform confirmation
and await a valid redelivery. HTTP 202 and these helpers do not keep a parked VM awake.

The [explicit version-4 example](../examples/event-acknowledgment-python/README.md#complete-with-a-processing-lease)
uses new leased jobs without relabeling older unleased or failure-decision records.
The host owns side-effect-free preparation, both relevant lease lifetimes and
exclusive fenced queue ownership. For coordinated preparation and both renewal
loops, use the separate
[`run_prepared_durable` lifecycle](event-acknowledgment-python-preparation.md).
External effects still require an outbox or their own idempotency protocol.

## Failure, cancellation and reconciliation

`EventConsumerProcessingError` has fixed code/message without driver diagnostics,
rejected SQL rows, tokens or original bodies:

| Code | Meaning |
| --- | --- |
| `event_consumer_processing_busy` | Another unexpired claim owns preparation. |
| `event_consumer_processing_lease_lost` | No matching unexpired claim can renew or complete new work. |
| `event_consumer_processing_operation_busy` | Another operation uses this capability object. |
| `event_consumer_processing_store` | A processing storage operation failed. |

Receipt/input conflicts use `EventConsumerConflictError`. Invalid arguments,
mismatched worker inputs and cross-loop use raise `ValueError`. Business callback
errors remain application-owned; sanitize them before public logging. Intent-hook
failures retain the existing intent error path; local fence loss retains its
processing code. A per-object guard does not grant distributed ownership. Expiry
cannot physically stop an old process; it fences final SQL commits.

Callbacks receive the worker's read-only `EventAcknowledgmentWorkerCancellation`
view. Native task or event cancellation signals the operation and joins active
SQL, cleanup and worker platform renewal, using `EventAcknowledgmentWorkerCancelled`.
It does not forcibly cancel driver calls or prove rollback. Use bounded driver
calls and cooperative handlers; drain work before closing pools/clients/loops.
Once COMMIT is sent, preserve confirmed success or unknown outcome even if cancelled.

`EventConsumerCommitUnknownError` closes the exclusively leased connection and
exposes no capability/SQL cause on leased paths. Discard it through the pool and
acquire a fresh idle autocommit connection:

- Unknown claim COMMIT returns no capability. Reclaim the same original request;
  a possibly committed claim can remain busy until expiry.
- Unknown renewal COMMIT must stop preparation until ownership is safely
  reconciled. It grants no confirmed new lease and establishes no rollback.
- Unknown completion COMMIT requires receipt-only reconciliation or repeated
  completion with the same request/capability. A committed receipt skips business
  work; without one, the local fence must still be valid.

Durable workers use `save_completion_intent` to reconcile a receipt without
preparation or local checks; immediate workers use `complete_receipt`. Missing
receipts require an explicit owner policy. Do not infer rollback or select
retry/dead-letter automatically from uncertainty. Retain jobs and immutable
decisions until the appropriate fenced handoff succeeds.

Failed preparation leaves the claim to expire, without automatic release, negative
acknowledgment or scheduling. Retain receipts/immutable claim input while stale
jobs or replay remain possible. No platform migration, endpoint or service is
added. Local validation covers generation, compilation, packaging and static
checks. Hosted concurrency, takeover, rollback, timing, cancellation, callbacks and
shared Go/Node/Python state acceptance remains pending before release.

## Inspect recovery evidence

[`ainspect_event_consumer_processing`](event-consumer-processing-inspection.md#python-inspection)
reads one original request's receipt/claim metadata with a single snapshot and
database clock, without tokens/results or ownership/mutations. Use it to diagnose
busy or interrupted jobs before the owner's explicit recovery decision. Matching
receipt metadata still requires ordinary receipt validation; absence/expiry proves
neither rollback nor stopped work.
