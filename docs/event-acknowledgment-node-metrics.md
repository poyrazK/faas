# Node preparation metrics

Python provides the same fixed counting and bucket contract through
[Python preparation metrics](event-acknowledgment-python-metrics.md), with detached
JSON-safe dictionaries and Python field names.

`EventAcknowledgmentPreparationMetrics` collects bounded counters and cumulative
duration histograms from [preparation lifecycle events](event-acknowledgment-node-telemetry.md).
Create one long-lived collector for an application-owned observation scope and
pass its bound `record` function directly as `onEvent`:

```js
import { EventAcknowledgmentPreparationMetrics } from '@gregale/sdk-node';

const metrics = new EventAcknowledgmentPreparationMetrics();

await worker.runPreparedDurable(pool, prepare, handler, {
  preparationTimeoutMs: 60000,
  processingLeaseMs: 30000,
  signal: job.signal,
  onEvent: metrics.record,
});

const snapshot = metrics.snapshot(); // Read later from your scrape/export path.
```

`record` is synchronous and receiver-independent. It performs no database query,
network call, timer, logging, callback delivery or recovery action. Counters have
fixed labels and histograms have fixed buckets. No event, identity, token, payload,
prepared value, result or error object is retained. No metrics dependency,
route, platform schema or background service is installed. A separate
[pure Prometheus formatter](event-acknowledgment-node-prometheus.md) can expose
snapshots from an application-owned scrape path.

## Snapshot fields and counting

Every snapshot is detached and recursively frozen, including counter maps,
histogram arrays and bucket objects. Older snapshots do not change when more
events arrive. Snapshots contain JSON-safe finite numbers, fixed string keys,
booleans and the final histogram bound sentinel `null`.

| Field | Updated from | Meaning |
| --- | --- | --- |
| `eventCounts` | Every accepted event | One counter per fixed event kind. |
| `operationOutcomes` | `shutdown` only | Observed prepared-operation outcomes: committed, commit_unknown, cancelled or failed. |
| `preparationOutcomes` | `preparation_finished` | Callback settlement observations: returned or threw. |
| `localCommits` | `intent_committed` | `newReceipt` versus `replayedReceipt`, following the event's replay flag. |
| `renewalFailures` | `renewal_failed` | Local/platform failures, split by the fixed reason vocabulary. |
| `unknownCommitsByStage` | `commit_unknown` | Uncertain local commit observations, split by the fixed stage vocabulary. |
| `preparationDuration` | `preparation_finished.durationMs` | Callback-settlement duration samples, including both returned and thrown callbacks. |
| `operationDuration` | `shutdown.elapsedMs` | Whole-operation duration samples, including cleanup, for all observed outcomes. |
| `saturated` | Arithmetic clipping | At least one counter, histogram bucket or sum exceeded the safe integer range. Sticky for the collector's lifetime. |

Counter maps include zeroes for every current label. Arbitrary labels are never
added from events. Operation outcomes come only from shutdown, so a late platform
renewal failure can increment its failure counter while the operation remains
committed. Local commit and replay counters describe receipt/intent transactions;
they do not establish platform callback success or unique business effects.

These are separate metric families observing the same lifecycle. Do not add
`intent_committed`, `commit_unknown`, renewal failures and shutdown outcomes into
a single operation total. A completed-receipt fast path can increment replay and
committed-operation counters without contributing a preparation-duration sample.
A returned preparation callback can still be followed by a failed final commit.

## Histograms and overflow

`EVENT_ACKNOWLEDGMENT_PREPARATION_DURATION_BUCKETS_MS` exports the frozen finite
inclusive bounds used for both histograms:

```text
1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000,
10000, 30000, 60000, 300000
```

Each histogram has `buckets`, `count`, `sumMs` and `saturated`. Bucket counts are
cumulative: each counts samples less than or equal to `upperBoundMs`. The final
bucket uses `upperBoundMs: null` for positive infinity and equals `count`. A zero
millisecond sample enters the first bucket. Durations retain the event contract's
rounded whole milliseconds; neither histogram estimates a lease budget.

All arithmetic stays within `Number.MAX_SAFE_INTEGER`. A value is clamped only
when an addition would lose an integer increment or duration. Histogram saturation
is local to that histogram; top-level saturation also covers ordinary counters.
Affected values then represent lower bounds on accepted observations. Saturation
does not reset, wrap, throw, stop collection or alter processing. Avoid derived
averages/rates/quantiles from clipped values. A false flag establishes only that
arithmetic has not clipped, not that the observation stream is complete.

## Export outside the observer

For Prometheus text 0.0.4, use
[`formatEventAcknowledgmentPreparationMetrics`](event-acknowledgment-node-prometheus.md).
It handles cumulative buckets, seconds, fixed labels and saturation gauges
without registering metrics or opening a listener.

Read snapshots on an application-owned scrape/export path. For Prometheus-style
histograms, publish cumulative bucket counts, `count` and `sumMs` as absolute
observations. Map `null` to the `+Inf` label; convert finite bounds and `sumMs` by
dividing by 1000 when exporting seconds. Choose an application-owned metric
namespace and fixed label mappings. Repeatedly incrementing an exporter counter
with the entire cumulative snapshot would double-count previous observations.

An OpenTelemetry adapter must support the appropriate explicit histogram
aggregation/temporality. Convert cumulative bucket counts to per-bucket counts
when required and handle exporter deltas/resets explicitly. Do not feed a whole
snapshot back as new `record` events or fabricate individual duration samples
from its mean. No OpenTelemetry adapter, network publication or automatic delta
conversion is provided by this helper.

## Validation and observation limits

`record` validates one complete current-contract event before mutating state.
Kinds, outcomes, reasons and stages must be known literals; durations must be
nonnegative safe integers and replay flags must be booleans. Required fields
must be own data properties. Unexpected fields, symbols, accessors, missing
fields, invalid values or throwing property-descriptor traps produce a generic
`TypeError` without retaining a raw cause. Property getters are not invoked.
The source object is copied into validated primitive metadata, then discarded.

The lifecycle hook isolates observer errors. A direct invalid `record` call
throws before that call updates any metric; when passed as `onEvent`, its error
is swallowed by the worker's existing observer boundary. Use compatible event
and collector versions and keep external adapters outside that callback.

Collection is process-local and cumulative from construction. Sharing one
collector across concurrent operations intentionally aggregates their events.
Duplicate or replayed inputs count again; there are no operation IDs, sequence
tracking, deduplication, active-work gauges, reset/restore/merge APIs or durable
checkpoints. Constructing a new collector starts at zero. Exporters own reset
detection and process identity.

Lost events, filtered observers, crashes and unfinished shutdowns can leave
counts incomplete. Do not infer in-flight work by subtracting start/shutdown
counters or treat metrics as a business-effect deduplication ledger. Callback
delivery and retained backlog remain covered separately by
[intent recovery health](event-acknowledgment-node-intents.md) and
[processing inspection](event-consumer-processing-inspection.md).

The [protected-job example](../examples/event-acknowledgment-node/README.md#collect-preparation-metrics)
wires the bound observer without changing job records or ownership. Local
validation covers compilation, generation, packaging and static checks. No tests
or assertions were added or changed and no local tests were run. Hosted acceptance
remains required for event validation, cumulative buckets, saturation, snapshot
isolation and lifecycle counting before release.
