# Inspect Node consumer processing recovery

`inspectEventConsumerProcessing(pool, input, { signal })` reads metadata for one
original consumer request from the application database. Use it to understand a
busy claim or interrupted job before choosing an explicit recovery action.
It returns no capability, claim token, event body, source/event ID, input digest
or saved result body.

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
