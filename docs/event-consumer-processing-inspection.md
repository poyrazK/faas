# Inspect consumer processing recovery

`inspectEventConsumerProcessing(pool, input, { signal })` reads metadata for one
original consumer request from the application database. Use it to understand a
busy claim or interrupted job before choosing an explicit recovery action.
It returns no capability, claim token, event body, source/event ID, input digest
or saved result body.

Python provides the same snapshot/classification protocol through
[`ainspect_event_consumer_processing`](#python-inspection), with frozen snake-case
metadata and async psycopg connection ownership.

Authenticate and authorize the diagnostic route first. Supply trusted account/app
scope and exact original CloudEvent bytes through an existing request constructor:

```js
import { eventConsumerRequestFromHeaders, inspectEventConsumerProcessing } from '@gregale/sdk-node';

const input = eventConsumerRequestFromHeaders(headers, originalBody, accountId, appId);
const evidence = await inspectEventConsumerProcessing(pool, input, { signal });
console.log(evidence); // Metadata for an operator, not job acknowledgment.
```

Install `eventConsumerReceiptSchema` and `eventConsumerProcessingSchema` explicitly
as the application database owner. The inspector uses the same Go/Node tables and
digest protocol as [processing leases](event-consumer-processing-node.md). It
requires no acknowledgment token, platform request, intent schema, migration or
new service. Use a read-authorized database role with access to both tables and
an application-owned pool. The caller must authorize the requested consumer and
retained original inputs; helpers do not provide database tenant authorization.

## Returned evidence

The frozen result contains primitive metadata only:

| Field | Meaning |
| --- | --- |
| `observedAt` | Database clock captured once for this statement, as UTC RFC3339 text with six fractional digits. |
| `receipt` | `absent`, `matching` or `conflict`, comparing owner, subscription, exact decimal acceptance ID, event source/ID and original-input digest. |
| `claim` | `absent`, `active`, `expired` or `conflict`. The identity digest scopes the lookup; a present row must match the original-input digest. |
| `state` | Combined interpretation: any conflict first, then matching receipt (`completed`), then matching claim (`active`/`expired`), otherwise `absent`. |
| `expiresAt` | Present only for a matching claim. UTC text retains PostgreSQL microsecond precision. |
| `remainingLeaseMs` | Present with `expiresAt`; nonnegative whole milliseconds, rounded down at the database observation, zero for expired claims. |

An expiry equal to the observation time is expired. An active claim with less than
one millisecond remaining reports `remainingLeaseMs: 0`; use `claim`/`state` for
the snapshot classification. Remaining time is advisory, never a safe sleep or
processing budget. The client clock and `Date` rounding do not decide expiry.

A matching receipt plus a matching retained claim reports `state: completed`
and preserves the claim's timing evidence. This can occur when an unleased writer
commits a receipt while a processing row remains. A receipt/input or claim/input
conflict takes precedence even when the other record matches. Conflicting claims
expose no expiry. The inspector neither repairs contradictory rows nor deletes
leftover claims.

`receipt: matching` and `state: completed` mean committed identity/input metadata
was observed. The saved response body is neither selected nor validated. Use the
ordinary receipt reconciliation/completion method to retrieve and validate the
saved result before acknowledging delivery. This inspector supplies no result
bytes and sends no terminal callback.

## Snapshot and concurrency boundaries

One parameterized `SELECT` uses one MVCC statement snapshot and one materialized
database clock. It performs two primary-key lookups, each bounded to the supplied
account/app/subscription/acceptance identity digest. Receipt identity comparisons
also verify stored owner, subscription, acceptance and event metadata. Acceptance
IDs stay canonical decimal strings through the PostgreSQL bigint parameter.

The query selects equality booleans and bounded timestamps only. It takes no
consumer advisory lock or processing-row lock and changes no receipt, claim,
expiry, retry or acknowledgment state. There is no enumeration or aggregate
scan across tenants or consumers. Tokens and receipt results are never selected,
including on conflict.

The snapshot can miss a transaction that has not committed or commits after the
statement snapshot was taken. Concurrent renewal, completion or takeover can
make the result stale immediately. An absent receipt does not prove rollback or
that business work stopped. An expired claim does not prove the old process
stopped. A wall-clock change can affect later observations. Neither inspection
nor waiting until expiry grants ownership; claim/renew/completion must still pass
their existing database fences. Use the authoritative application database when
diagnosing uncertain commits; replica lag adds another source of stale evidence.

## Choose recovery explicitly

| Combined state | Operator interpretation and next step |
| --- | --- |
| `completed` | Reconcile the validated receipt with `worker.saveCompletionIntent` in durable mode, or the existing completion path for the job's protocol. Keep callback recovery independent of business work. |
| `active` | A matching unexpired claim was observed. Keep the job under its owner policy; inspect again as needed without stealing or extending the claim. |
| `expired` | A matching expired claim was observed. An exclusively owned restart may attempt the ordinary claim/coordinator path; its live fences decide admission. |
| `absent` | No retained receipt or claim was observed. Reconcile original inputs, uncertain transactions, retention and job ownership before permitting new work. |
| `conflict` | Investigate immutable input/identity disagreement. Preserve the evidence; do not overwrite or delete rows to make recovery pass. |

The inspector schedules nothing, sends no retry/dead-letter action, acks no job
and releases no claim. Persist any recovery decision through the existing
job-owner protocol. See [coordinated preparation](event-acknowledgment-node-preparation.md)
and the [protected-job example](../examples/event-acknowledgment-node/README.md#inspect-a-processing-job).

## Failures and cancellation

Invalid options, pool shape or unconstructed requests throw `TypeError` before
database access. Storage/query failures, malformed evidence and invalid or
unrepresentable timestamps use the existing bounded
`EventConsumerProcessingError` with code `event_consumer_processing_store`.
No raw SQL error, rejected row or driver diagnostic is retained. A storage error
is unavailable evidence, not an `absent` observation. Missing schemas remain a
storage failure; the helper never installs them or silently falls back.

Cancellation is cooperative before and after the query. Active acquisition/query
calls are awaited before releasing the connection; configure acquisition, query
and statement timeouts. An owner-supplied abort reason remains application-owned.
Query failures discard the connection. The inspector sends no transaction commit
or platform mutation and adds no unknown-commit recovery state.

Local validation covers Node compilation, API generation, package/declaration
inclusion and static source/documentation checks. No tests or assertions were
added or changed and no local tests were run. Hosted database acceptance remains
required before release for concurrent commit/renewal/takeover snapshots, conflict
precedence, timestamp boundaries, cancellation and metadata-only query behavior.

## Python inspection

`ainspect_event_consumer_processing(connection, request, stop_event=...)` reads
the same shared processing tables and identity/input digests. It uses one
parameterized metadata `SELECT`, with SQL identical to Node after adapting repeated
positional bindings. It sends no `BEGIN`, `COMMIT`, advisory/row-lock statement,
platform call or mutation. Neither a platform client nor an acknowledgment token
is needed. Explicitly install `event_consumer_receipt_schema` and
`event_consumer_processing_schema`; missing schemas are unavailable evidence,
never an absent/healthy result.

Authenticate and authorize diagnostic access first. Supply exact original bytes
and trusted owner scope through the existing request constructor:

```python
from dataclasses import asdict
from faas_sdk import ainspect_event_consumer_processing, event_consumer_request_from_headers

request = event_consumer_request_from_headers(headers, original_body, account_id, app_id)
async with diagnostic_pool.connection() as connection:
    evidence = await ainspect_event_consumer_processing(
        connection, request, stop_event=shutdown,
    )
record_processing_evidence(asdict(evidence))
# An operator observation, never permission to acknowledge or start work.
```

Use an exclusively leased idle psycopg async connection with `autocommit=True`.
The application owns acquisition and must bound acquisition, query, statement and
cleanup calls, discard closed connections and drain reads before closing its pool
or event loop. Use a read-authorized role with access to both tables and trusted
diagnostic routing; SDK helpers provide no database tenant authorization. Keep
diagnostic connections separate from active preparation, completion and recovery.

`EventConsumerProcessingInspection` is frozen and contains primitives only:

| Python field | Meaning |
| --- | --- |
| `state` | `conflict` first, then matching receipt as `completed`, then matching claim as `active`/`expired`, otherwise `absent`. |
| `observed_at` | One database-clock observation, canonical UTC RFC3339 with six fractional digits. |
| `receipt` | `absent`, `matching` or `conflict`, including full owner/consumer/acceptance/source/event/input comparison. |
| `claim` | `absent`, `active`, `expired` or `conflict`, requiring the original input digest. |
| `expires_at` | Canonical UTC text only for a matching claim; otherwise `None`. |
| `remaining_lease_ms` | Nonnegative integer milliseconds rounded down from the database observation; otherwise `None`. Expired claims report zero. |

Python represents absent optional metadata as `None` (including in `asdict`), where
Node omits it. Classification otherwise follows the same protocol. Timestamps
must be valid canonical UTC text in years 0001–9999. Integer arithmetic preserves
microseconds without floating-point/client-clock rounding. Expiry equal to the
observation is expired. An active claim with less than one millisecond remaining
reports zero remaining milliseconds; inspect `claim` rather than treating zero
as expiry. Remaining time is advisory, never a safe sleep or processing budget.

A matching receipt plus matching retained claim reports `completed` and retains
claim timing. Any conflict wins, including a claim conflict alongside a matching
receipt; conflicting claims expose no expiry. The query neither selects nor
validates saved result JSON. Use `save_completion_intent` or `complete_receipt`
to validate/replay the result before the job's appropriate acknowledgment path.
No claim capability is returned and no job is acknowledged by inspection.

Invalid/unconstructed requests and non-idle connections raise `ValueError` through
the existing request contract. Invalid stop events are rejected before SQL.
Query/cursor failures or malformed evidence use the fixed
`EventConsumerProcessingError` code `event_consumer_processing_store`, without raw
SQL diagnostics, rejected rows or causes. Query/cursor failures close the leased
connection; discard it through the pool. Malformed evidence is unavailable, not
an absent state, and is never silently repaired or coerced.

Native/event cancellation uses `EventAcknowledgmentWorkerCancelled` and joins
active SQL and cursor cleanup. It does not forcibly cancel a query or prove that
business work rolled back. A driver-level cancellation closes the connection;
cooperative owner cancellation after a successful read need not discard a healthy
connection. This read introduces no unknown-COMMIT state because it sends no
commit or mutation.

Concurrent commit, renewal and takeover can make this snapshot stale immediately.
An absent receipt proves no rollback, and an expired claim proves no stopped
process. Neither inspection nor waiting until expiry grants ownership. Use the
authoritative application database and existing live claim/completion fences;
replica lag and retention add uncertainty. The owner must select/persist recovery
decisions explicitly, without deleting contradictory evidence or automatically
issuing retry/dead-letter.

The [Python protected-job example](../examples/event-acknowledgment-python/README.md#inspect-a-processing-job)
adds read-only helpers for explicit manually leased and coordinated job modes.
Local validation covers compilation/lint, Python 3.11 syntax, SQL source parity,
isolated generation, wheel/sdist inclusion and static checks. No tests/assertions
are added/changed and no local tests run. Hosted snapshot concurrency, conflict
precedence, microsecond boundaries, cancellation and metadata-only acceptance
remains pending before release.
