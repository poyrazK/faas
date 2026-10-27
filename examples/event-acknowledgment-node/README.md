# Durable Node event acknowledgment integration

[`stored-policy.mjs`](stored-policy.mjs) shows explicit atomic policy binding for
new completion intents. Apply the shared upgrade and update every recovery host
before using it. Repeated explicit staging must use the original normalized
policy; existing unbound duplicates conflict. See the
[shared binding contract](../../docs/event-acknowledgment-stored-retry-policy.md).

This module connects the [Node durable intent store](../../docs/event-acknowledgment-node-intents.md)
to an application-owned durable ingestion queue and a separate recovery host.
Copy/import `consumer.mjs` into those hosts. No helper starts automatically, and
the module supplies no queue/storage implementation.

Install the Node SDK, configure one `FaaSClient` with `apps:read` and `deploy:write`
(or admin) credentials, and explicitly install `eventConsumerReceiptSchema` and
`eventAcknowledgmentIntentSchema` in the business PostgreSQL database. Restrict
intent table and backup access: pending rows contain the raw token and result.
Configure bounded pool acquisition, query and statement timeouts. Enable deferred
acknowledgment only after compatible schedulers are deployed, as described by the
[platform protocol](../../docs/event-deferred-acknowledgments.md).

## Queue adapter

`queue.enqueueProtected(record)` durably commits the entire record before
resolving. Encrypt/restrict job, backup and queue-console access: selected delivery
headers contain the raw token and the body is application data. The random job ID
is independent of the token. Never log or expose the record. Ordinary bearer
credentials are excluded.

The worker host supplies an exclusively owned `job` with:

- `record`: the exact saved version 2 `durable-intent` record, including immutable owner scope/headers/body.
- `signal`: aborted when ownership or the worker's processing deadline is lost.
- `ack()`: an ownership-fenced durable deletion/acknowledgment.

Keep failed/unacknowledged jobs across restarts and preserve exclusive ownership.
The SDK's object guard is not a distributed lease. The example accepts only new
version 2 records so old immediate-mode jobs cannot silently change recovery mode.
Drain or explicitly reconcile old version 1 jobs with their original protocol;
do not relabel them, mix modes on an attempt or reset an old recovery phase.
Retain receipts throughout possible retry/replay and retain intent metadata while
stale jobs can return. No helper prunes these tables.

## Authenticated ingress

After the existing route authenticates and authorizes the internal request,
captures bounded original bytes without JSON reserialization and loads trusted
owner configuration:

```js
import { acceptAuthenticatedDelivery } from './consumer.mjs';

const response = await acceptAuthenticatedDelivery(req.headers, originalBody, {
  app: appSlug, accountId, appId,
}, queue);
res.writeHead(response.statusCode, { 'content-type': 'application/json' });
res.end(response.body);
```

Reject a failed enqueue/factory call instead of sending 202. Header parsing is not
authentication. Saved owner scope is compared to current trusted configuration
before processing, preventing accidental reuse after host reconfiguration.

## Owned processing worker

```js
import { processOwnedDelivery } from './consumer.mjs';

await processOwnedDelivery(job, { app: appSlug, accountId, appId }, pool,
  async (tx, signal, originalBody) => {
    signal.throwIfAborted();
    const event = JSON.parse(originalBody.toString('utf8'));
    // Apply application-specific validation/authorization before writes.
    await tx.query('UPDATE invoices SET paid=true WHERE id=$1', [event.data.invoice_id]);
    return JSON.stringify({ applied: true });
  });
```

Use only transactional writes through `tx` in this callback. `runDurable` commits
business writes, the consumer receipt and completion intent together. The job is
acked after this local commit; that transfers callback delivery responsibility to
the durable intent recovery host. It does not confirm the platform acknowledgment.

A restart after commit replays the receipt and same intent without invoking the
handler. A retry after an uncertain COMMIT waits for the consumer identity lock;
new business work requires confirmed platform renewal and no committed receipt.
For an explicit receipt-only policy use `saveCompletionIntent`; if the receipt is
missing, retain the job for the owner's recovery policy. Do not infer rollback
from COMMIT uncertainty or automatically convert errors into negative callbacks.
Explicit negative decisions require stopped work, known rollback, exclusive
ownership and the same persisted action/reason on subsequent saves.

## Independent intent recovery host

Run a bounded iteration from a durable host that can reach PostgreSQL and apid:

```js
import { recoverOneAcknowledgment } from './consumer.mjs';

const status = await recoverOneAcknowledgment({ app: appSlug, accountId, appId }, pool, signal);
if (status) {
  // Metadata only. Inspect state/lastCode; do not treat resolution as confirmation.
  reportAcknowledgmentStatus(status);
}
```

The host owns scheduling, shutdown and concurrency. `undefined` means no due,
unclaimed row. `unconfirmed` has a database-time retry scheduled; `superseded`
stops this row. Storage/claim/settlement errors can leave a lease until expiry.
Recovery never executes business code and sends only the saved callback. Terminal
rows clear their raw token/result/claim, while backups and in-memory claims remain
protected. Expired platform leases can require a new delivery; receipt identity
still keeps consumers independent and prevents repeated committed writes.

The module's concurrency/restart behavior requires hosted acceptance coverage
before release. Local validation is generation, SDK build and static checks.

## Observe recovery health

The recovery host or an independent monitoring process can read a bounded summary:

```js
import { readAcknowledgmentRecoveryHealth } from './consumer.mjs';

const health = await readAcknowledgmentRecoveryHealth({ app: appSlug, accountId, appId }, pool, signal);
recordRecoveryHealth(health); // Include complete, truncated and coverage with every metric.
```

This reads one snapshot of retained unfinished intents; it does not claim work or
send callbacks. Defaults are 1000 sampled rows and a five-minute stall interval.
`due` excludes live claims, and `stalled` starts aging only after both the next
retry time and any claim expiry. Future schedules and live leases are not stalls.
An extra row marks `truncated`; invalid timestamp metadata makes `complete` false.
A zero stalled count in an incomplete sample cannot establish healthy recovery.
Oldest ages describe the sample and `observedAt` comes from the database clock.

The host owns observation scheduling and metrics/notification transport. The read
helper starts no poller or alerts. See the [Node health options and coverage](../../docs/event-acknowledgment-intent-health.md#node-summary).

## Watch sustained recovery conditions

Start a separate monitoring watch explicitly from the host, using a signal that
is aborted on shutdown:

```js
import { watchAcknowledgmentRecovery } from './consumer.mjs';

await watchAcknowledgmentRecovery({ app: appSlug, accountId, appId }, pool, signal,
  async (alert, callbackSignal) => {
    callbackSignal.throwIfAborted();
    await recordRecoveryAlertLocally(alert, callbackSignal);
  });
```

The wrapper enables a due threshold of 100, stalled threshold of 1 and parked
threshold of 1, with
30-second confirmation for both firing and resolution. It reads metadata only,
uses the shared five-minute stall definition and claims no recovery work. Both
transitions need multiple fresh, complete, advancing observations. Missing,
truncated, stale or out-of-order evidence resets proof and emits `unknown` or
`confirmation_lost`; it never proves an incident resolved.

Callbacks are local, sequential and awaited. The host owns their persistence and
notification transport. Failure stops the watch with the failed metadata alert;
notification uncertainty is not automatically retried. Cancellation rejects with
the signal reason, without resolving an alert. Configure actual acquisition/query
and statement timeouts: SDK cancellation is cooperative and waits for active
operations rather than overlapping polls or releasing a busy connection. Watch
state is in memory and resets on restart. See [Node watch configuration and recovery](../../docs/event-acknowledgment-intent-health.md#node-watch).

## Persist recovery alert checkpoints

Use `watchAcknowledgmentRecoveryWithCheckpoint` as an explicit alternative to the
ordinary in-memory watch:

```js
import { watchAcknowledgmentRecoveryWithCheckpoint } from './consumer.mjs';

await watchAcknowledgmentRecoveryWithCheckpoint(
  { app: appSlug, accountId, appId }, pool, signal, monitorStore,
  deliverRecoveryNotificationIdempotently,
);
```

The application-owned `monitorStore` must provide:

- `loadOwned(signal)`: load the saved checkpoint under exclusive writer ownership.
- `saveOwned(checkpoint, signal)`: atomically commit the whole checkpoint and reject stale writers.
- `listAcknowledgments(checkpoint, signal)`: return at most 1024 unique exact pairs still present in that checkpoint's pending/retained acknowledgment lists; keep the ledger durable until checkpoint persistence records them.
- `recordAcknowledgment(pair, signal)`: durably record destination confirmation, idempotently.
- `saveOwnedOnShutdown(checkpoint)`: use an independent bounded shutdown context and the same ownership fence; an aborted or stale writer must not overwrite a successor.

The wrapper saves each snapshot before attempting notification, including initial
replay before the first health query. It delivers `checkpoint.pending` in sequence
order. The destination must deduplicate by incident ID plus sequence. A crash after
confirmed delivery but before acknowledgment can repeat that same notification.
No queue, store, file or notification service is installed by the SDK.

The wrapper enables the same three thresholds as the ordinary watch, selecting
a version 2 checkpoint for parked alerts. Every host that can resume or acknowledge
it must support version 2. Omit `parkedThreshold` or set it to zero to retain the
previous version 1 selection. Changing the selection requires a new session after
reconciling and retaining the old checkpoint/incidents/notifications under the
writer fence; do not edit a saved version in place. A parked recovery notification
means only that parked count remained below threshold, including after operator
resume or supersession. See the
[parked alert contract](../../docs/event-acknowledgment-node-checkpoints.md#parked-intent-alerts).

Retain unresolved identity across evidence loss and restart, then collect fresh,
complete observations before reconfirming or resolving. The outbox contains only
opening/resolution lifecycle notifications; unknown evidence remains visible in
progress. A full outbox blocks new openings and reserves capacity for existing
resolutions. The ordinary watch's state is not a checkpoint and cannot be resumed
as one. Do not run two writers for the same checkpoint or apply offline
acknowledgments concurrently with a live writer.

A callback failure throws `EventAcknowledgmentIntentCheckpointWatchCallbackError`
with a detached result. The wrapper does not overwrite storage in that case:
compare/reconcile current storage and `error.result.checkpoint` under the ownership
fence before notification retry. Cancellation returns a final snapshot which the
wrapper saves with its bounded shutdown hook. See [checkpoint, acknowledgment and replay rules](../../docs/event-acknowledgment-node-checkpoints.md).

## Prepare with a processing lease

`processing.mjs` is an explicit alternative for consumers doing side-effect-free
preparation before a short final transaction. Install `eventConsumerProcessingSchema`
as well as the receipt/intent schemas. Ingress calls
`acceptAuthenticatedDeliveryForPreparation` after authentication/authorization;
it uses the same protected durable queue contract but saves version 3 records
with mode `durable-intent-processing`. Keep version 2 jobs on the existing
unleased flow. Do not relabel jobs or mix leased and unleased processing for the
same consumer.

```js
import { processOwnedPreparedDelivery } from './processing.mjs';

await processOwnedPreparedDelivery(job, { app: appSlug, accountId, appId }, pool,
  async (originalBody, { signal, renew }) => {
    signal.throwIfAborted();
    const event = JSON.parse(originalBody.toString('utf8'));
    const prepared = await readAndValidateWithoutEffects(event, signal);
    await renew(); // Serialize local renewals and stop/join all work before returning.
    return prepared;
  }, async (tx, signal, _originalBody, prepared) => {
    signal.throwIfAborted();
    await tx.query('UPDATE invoices SET paid=true WHERE id=$1', [prepared.invoiceId]);
    return '{"applied":true}';
  });
```

Claiming a committed receipt skips preparation and the handler. Otherwise the
opaque local token fences the final callback and conditional claim removal. The
job is acked after writes, receipt, claim removal and completion intent commit;
the independent intent host delivers the platform callback.

The example renews the local lease before/after preparation and defaults to
30 seconds. Preparation owns its renewal cadence, must remain side-effect-free,
and must return only after its work/renewals stop. A busy claim or any renewal
failure leaves the job for the owner's recovery policy; no negative disposition
is inferred. Reconstructed jobs reclaim after expiry, and committed receipts
continue to deduplicate writes.

Local renewal does not extend the platform acknowledgment lease. Bound preparation
to its remaining budget or maintain that platform lease separately under trusted
host ownership. The worker confirms/renews the platform lease only during final
transactional work. Configure driver/query/statement timeouts and job cancellation;
this module installs no renewal loop or worker service. See [processing lease semantics and unknown commits](../../docs/event-consumer-processing-node.md).

## Coordinate both leases during preparation

For new jobs whose preparation needs local and platform renewal, explicitly use
`acceptAuthenticatedDeliveryForCoordinatedPreparation` from `processing.mjs`
after authentication/authorization. It persists version 4 records with mode
`durable-intent-prepared`. Keep version 2 unleased jobs and version 3 manual lease
jobs on their existing paths; do not relabel records or run multiple modes for
the same owned job.

```js
import { processOwnedCoordinatedDelivery } from './processing.mjs';

await processOwnedCoordinatedDelivery(job, { app: appSlug, accountId, appId }, pool,
  async (originalBody, { signal }) => {
    signal.throwIfAborted();
    const event = JSON.parse(originalBody.toString('utf8'));
    return await readAndValidateWithoutEffects(event, signal);
  }, async (tx, signal, _originalBody, prepared) => {
    signal.throwIfAborted();
    await tx.query('UPDATE invoices SET paid=true WHERE id=$1', [prepared.invoiceId]);
    return '{"applied":true}';
  }, { preparationTimeoutMs: 60000, processingLeaseMs: 30000 });
```

`runPreparedDurable` owns both renewal loops and joins local renewal before the
short final transaction. Do not start manual renewal or detached preparation
work. The wrapper uses the job's ownership signal for the entire operation and
acks the job only after confirmed local receipt/intent commit. Existing receipts
skip preparation and business code; the independent intent host handles callbacks.
Retain the job after failures or uncertainty and follow explicit owner recovery.

The required preparation budget covers startup, claim, preparation and the local
renewal handoff. It does not bound the final transaction. Configure job deadlines,
pool/query/statement timeouts and a host that remains available; neither HTTP 202
nor timers keep a parked VM awake. See [lease loss, cancellation and uncertain commits](../../docs/event-acknowledgment-node-preparation.md).

## Inspect a processing job

`inspectOwnedProcessingDelivery` in `processing.mjs` reads metadata for a protected
version 3 manual or version 4 coordinated job after checking trusted owner scope,
mode and original bytes. It requires the job's ownership/cancellation signal but
does not reconstruct an acknowledgment capability or validate its token:

```js
import { inspectOwnedProcessingDelivery } from './processing.mjs';

const evidence = await inspectOwnedProcessingDelivery(
  job, { app: appSlug, accountId, appId }, pool,
);
console.log(evidence); // Only bounded receipt/claim evidence and timing metadata.
```

The helper invokes no preparation, business callback, platform request or job
acknowledgment. Keep the protected job and its original processing mode. A
`completed` observation leads to the owner's existing receipt reconciliation
path, which validates the saved result before staging or sending completion.
An active/expired/absent observation does not prove work stopped, a transaction
rolled back or a restart may commit. Conflicts require investigation. The owner
chooses recovery explicitly, and live claim/completion fences decide admission.
See [inspection states and snapshot limits](../../docs/event-consumer-processing-inspection.md).

## Observe coordinated preparation

`processOwnedCoordinatedDelivery` forwards `onEvent` in its options while retaining
the owned job's cancellation signal. Existing version 4 records need no change:

```js
await processOwnedCoordinatedDelivery(job, scope, pool, prepare, handler, {
  preparationTimeoutMs: 60000,
  processingLeaseMs: 30000,
  onEvent: event => { diagnosticQueue.tryEnqueue(event); },
});
```

`diagnosticQueue` is your bounded, synchronous adapter; a full queue may drop an
event. Drain it independently. The callback must return promptly and remain
passive: do not ack the job, renew claims, close resources or start recovery from
it. Events contain only fixed phase/outcome metadata and monotonic durations,
including first renewal failure per lease and confirmed or unknown local commits.
No protected record, identity, token, prepared value, result or raw error is passed.

Observer throws and accidental promise rejections are ignored; notification is
never awaited or retried. A confirmed receipt/intent commit remains confirmed
after late cancellation or platform renewal loss. `shutdown` means internal
renewal work joined, not that observer promises or the platform callback finished.
Use retained receipt/intent evidence for recovery. See [the lifecycle contract](../../docs/event-acknowledgment-node-telemetry.md).

## Collect preparation metrics

Create a long-lived collector at worker-host initialization and pass its bound
observer directly to the existing coordinated-job wrapper:

```js
import { EventAcknowledgmentPreparationMetrics } from '@gregale/sdk-node/preview';
import { processOwnedCoordinatedDelivery } from './processing.mjs';

const preparationMetrics = new EventAcknowledgmentPreparationMetrics();

await processOwnedCoordinatedDelivery(job, scope, pool, prepare, handler, {
  preparationTimeoutMs: 60000,
  processingLeaseMs: 30000,
  onEvent: preparationMetrics.record,
});

// Read from an independent scrape/export path; export outside processing callbacks.
const snapshot = preparationMetrics.snapshot();
```

Snapshots contain fixed counters and cumulative duration buckets, with detached,
recursively frozen data. Operation outcomes count shutdown events; local commit
and receipt replay observations stay separate from platform callback delivery.
Use the same collector across concurrent jobs only when that aggregation scope
is intended. Existing version 4 records and ownership signals need no change.

The collector retains no jobs, event IDs, tokens, prepared values, results or
errors. Recording duplicate events counts again. New instances start at zero,
and saturation flags indicate safe-integer clipping. The application owns exporter
temporality, units and process reset detection; no network/export loop is added.
See [histograms, saturation and observation coverage](../../docs/event-acknowledgment-node-metrics.md).

## Expose preparation metrics to Prometheus

`metrics.mjs` exports an explicit server factory. Use the same long-lived
`preparationMetrics` collector passed to `onEvent` above, and bind the scrape
listener on loopback in the worker host:

```js
import { createPreparationMetricsServer } from './metrics.mjs';

const metricsServer = createPreparationMetricsServer(preparationMetrics);
metricsServer.on('error', reportListenerFailure); // Your host's listener error handler.
metricsServer.listen({ host: '127.0.0.1', port: 9464 });
// During host shutdown, close the listener using metricsServer.close(...).
```

Nothing starts on module import. This server serves only `GET /metrics`, returns
404 for other paths and 405 for other methods on that path, and generates a full
snapshot body before sending 200. Collection/format failures return a generic 503
without raw error details. Successful scrapes use Prometheus text 0.0.4; all
responses disable caching.

Configure a same-host Prometheus scrape target at `127.0.0.1:9464` or use your
existing authenticated proxy/protected transport for remote collection. Keep this
listener separate from delivery ingress; no authentication middleware is supplied.
Each host/collector has its own cumulative observation stream and reset boundary.
Do not construct a collector per scrape or record the same snapshot as new events.

The formatter has no dependency on a metrics library and performs no processing,
renewal or callback action. See [metric families, seconds, saturation and validation](../../docs/event-acknowledgment-node-prometheus.md).

The [shared Node/Python monitoring pack](../event-acknowledgment-monitoring/README.md) adds a scrape configuration,
recording rules, warning alerts, a Grafana dashboard and recovery guidance for
these observations. Install it explicitly in your application monitoring stack
and tune its workload thresholds and notification routing before enabling it.

## Recover a bounded callback pass

`consumer.mjs` exports `recoverAcknowledgmentsPass(scope, pool, signal)`, using
the trusted owner scope, up to 100 admitted iterations and concurrency 4. It
recovers existing saved callbacks independently of ingestion-job processing.
Its retry policy selects exponential full jitter with a one-second initial cap
and a five-minute maximum. The claim count and due time persist in PostgreSQL.
For a fixed delay, omit `retryPolicy` and supply `backoffMs: 1000` instead.

```js
import { recoverAcknowledgmentsPass } from './consumer.mjs';
import {
  EventAcknowledgmentIntentRecoveryPassCancelled,
  EventAcknowledgmentIntentRecoveryPassCommitUnknownError,
  EventAcknowledgmentIntentRecoveryPassError,
} from '@gregale/sdk-node/preview';

try {
  const progress = await recoverAcknowledgmentsPass(scope, pool, shutdown.signal);
  recordRecoveryProgress(progress); // Fixed aggregate metadata, outside processing callbacks.
} catch (error) {
  if (error instanceof EventAcknowledgmentIntentRecoveryPassCommitUnknownError
    || error instanceof EventAcknowledgmentIntentRecoveryPassCancelled
    || error instanceof EventAcknowledgmentIntentRecoveryPassError) {
    recordRecoveryProgress(error.result);
  }
  throw error; // Preserve uncertainty/abort; the host chooses recovery and future scheduling.
}
```

Use the existing dedicated-client pool contract with promise-returning queries
and synchronous `release(discard?)`. The host bounds acquisition/query calls,
owns pool/client availability and waits for the pass before closing resources.
The primitive leases short transactions separately and releases before HTTP.
The pass guards release exceptions, preserving known settlements and unknown
COMMIT, and reports cleanup failures without retrying an ambiguous release.

An empty probe stops admission without proving the backlog drained; other hosts
can hold claims and work can become due later. Unconfirmed callbacks persist the
selected jittered delay and can become due again within a pass. Admissions
count iterations, including empty probes/acquisition failures, rather than unique
rows or HTTP requests. The host explicitly schedules another pass as needed.

Frozen progress contains no job IDs, tokens, payloads, saved results or raw
diagnostics. Caller abort reasons are replaced with a fixed internal reason.
Unknown COMMIT precedes AbortError, then ordinary failure. A selected generation
above Node's safe-integer range stops with the existing compatibility code when
ordinary failure is primary; use a compatible recovery host. The example never
acks ingestion jobs or reruns business work. See the
[bounded Node pass contract](../../docs/event-acknowledgment-node-recovery-pass.md).
See [retry policy bounds and uncertainty](../../docs/event-acknowledgment-node-retry-policy.md)
for database-clock scheduling and coordinated worker configuration.

## Adopt a policy for existing callback backlog

`policy-adoption.mjs` exports `selectPolicyAdoption` and `applyPolicyAdoption`.
The first reads an explicitly selected key with exact microsecond timestamp
metadata and builds an immutable command. Durably persist that command under its
trusted owner scope before calling the second function. Retry the same saved
command to resolve an uncertain result; selecting again is a new operator decision
and needs a new command UUID. No action occurs on module import.

Install the optional `eventAcknowledgmentIntentPolicyAdoptionSchema` alongside
the upgraded intent table. All recovery hosts must honor bindings before adoption.
Guard conflicts and missing rows commit rejection receipts; inspect typed errors'
`.receipt`. Adoption changes only policy columns and the timestamp, preserving
callback data, count and due time. See the
[shared adoption contract](../../docs/event-acknowledgment-policy-adoption.md#node-selection-adoption-and-replay).

## Bound recovery claims and resume parked callbacks

[retry-budget.mjs](retry-budget.mjs) shows optional `retryBudget` staging, precise
metadata selection and execution of a durably saved operator command. Install the
budget upgrade before current helpers (even v1-only use); install the optional
resume ledger before commands. Omitted budgets stay unlimited. Counts track
committed claims, including crashes, and never reset on resume. Known parked
outcomes appear in recovery progress/health; enable `parkedThreshold` for sustained
parked alerts. Keep enabled scopes on current budget-aware Go, Node or Python hosts. See
[installation, parking, exact fences and receipt replay](../../docs/event-acknowledgment-retry-budgets.md#node-budgets-and-guarded-resume).

## Adopt budgets for existing backlog

[budget-adoption.mjs](budget-adoption.mjs) selects precise metadata and constructs
a detached command that preserves v1 timing. Authorize the owner/key and total,
durably save all original inputs, then call its apply function. Install the separate
optional budget-adoption ledger first. Live claims, newer attempts or stale fences
produce retained conflicts; exact retries read historical results before current
rows, without another COMMIT. Counts, callbacks and due times remain unchanged.
See [Node adoption and receipt replay](../../docs/event-acknowledgment-budget-adoption.md#node-selection-adoption-and-replay).

## Export recovery health to Prometheus

[`createRecoveryHealthMetricsHost`](recovery-metrics.mjs) creates a bounded cache,
an unbound HTTP server and an explicitly invoked sequential poller. It observes
one trusted application scope and performs no callback recovery. Scrapes format
the cache only; no scrape starts SQL or a second poller.

```js
import { once } from 'node:events';
import { createRecoveryHealthMetricsHost } from './recovery-metrics.mjs';

const host = createRecoveryHealthMetricsHost(pool, accountId, appId);
host.server.listen(9466, '127.0.0.1');
await once(host.server, 'listening');
try {
  await host.run(shutdown.signal); // Host aborts on shutdown; work is joined before rejection.
} finally {
  await new Promise((resolve, reject) => host.server.close(error => error ? reject(error) : resolve()));
}
```

Use the SDK's Node version requirement (22.10+) and bound pool acquisition,
statement, connection release and HTTP shutdown. One run per host is permitted.
The loop reads immediately, then waits five seconds after each joined read.
A cooperative five-second query deadline is checked after all active driver work;
late/failing reads clear the cache. Thirty-second freshness expires even if the
poller hangs and scraping remains responsive. Stopping the poller clears it too.
Drain polling before closing the pool. Run the metrics host independently of
business request lifetimes; importing this file opens no listener or connection.

Valid incomplete/stale samples expose bounded raw counts with ineligible evidence.
Missing/failed observations omit counts rather than inventing healthy zeroes.
The default prefix and fixed gauge names match the
[recovery rules/dashboard](../event-acknowledgment-monitoring/README.md#recovery-health-node).
Choose one scrape job per collector, separate ports for other metric servers and
an explicit protected network path if Prometheus is in another namespace. No
account/invocation IDs or protected inputs appear in labels. Keep the scope-to-
target mapping in trusted application configuration.

See [cache, freshness and exposition details](../../docs/event-acknowledgment-node-recovery-prometheus.md)
and [operator recovery](../../docs/runbooks/GregaleEventRecovery.md). Live scraping,
clock behavior, alert evaluation and Grafana rendering remain pending hosted
qualification. No local tests or SDK helper smoke runs are used for this example.

## Observe actual recovery worker activity

[worker-metrics.mjs](worker-metrics.mjs) composes the health-only host with an
observer of actual bounded recovery passes. Construct it once per trusted owner
scope, explicitly bind its server to loopback 9466, supervise `runHealth(signal)`
and schedule `recover(signal)` separately. Inspect each original result or typed
exception before deciding whether to schedule another pass. Construction and
scraping start no recovery work.

Use the alternative
[worker scrape job](../event-acknowledgment-monitoring/prometheus-recovery-node-worker.yml)
for this endpoint, retaining both opt-in labels. Do not also scrape the same
cache through the health-only job. Idle/active expectations observe scheduling
and joined return; they do not cancel work or authorize retries. Join recovery,
polling and HTTP before closing the pool. See
[worker timing and native cleanup outcomes](../../docs/event-acknowledgment-node-worker-liveness.md)
and the [operator runbook](../../docs/runbooks/GregaleEventRecovery.md#node-worker-hosts-and-cleanup).
