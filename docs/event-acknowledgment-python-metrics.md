# Python preparation metrics

`EventAcknowledgmentPreparationMetrics` collects fixed counters and duration
histograms from [preparation lifecycle events](event-acknowledgment-python-telemetry.md).
Create one long-lived collector for an application-owned observation scope and
pass its bound `record` method as the synchronous `on_event` observer:

```python
from faas_sdk import (
    EventAcknowledgmentPreparationMetrics,
    EventAcknowledgmentPreparedOptions,
)

metrics = EventAcknowledgmentPreparationMetrics()
options = EventAcknowledgmentPreparedOptions(60_000, on_event=metrics.record)

await worker.run_prepared_durable(
    connection_scope, prepare, handler, options, stop_event=job.stop_event,
)

# Read later from the host's scrape/export path, outside the observer.
snapshot = metrics.snapshot()
```

The collector performs no SQL, network calls, logging, timers or recovery. It
retains fixed-size primitive totals, without retaining events, identities, tokens,
payloads, prepared values, results or error objects. No exporter dependency,
listener, background task, platform route, schema or registry is installed.

## Snapshot fields and counting

Snapshots are fresh detached JSON-safe dictionaries and lists. Their numbers,
string keys, booleans and final bucket sentinel `None` serialize directly to JSON.
They are mutable: changing a snapshot changes neither the collector nor any
earlier snapshot. Further observations never change an existing snapshot.

| Field | Updated from | Meaning |
| --- | --- | --- |
| `event_counts` | Every accepted event | One counter per fixed event kind. |
| `operation_outcomes` | `shutdown` only | Observed committed, commit_unknown, cancelled or failed operations. |
| `preparation_outcomes` | `preparation_finished` | Callback settlement: returned or threw. |
| `local_commits` | `intent_committed` | `new_receipt` versus `replayed_receipt`, following the replay flag. |
| `renewal_failures` | `renewal_failed` | Local/platform failures split by the fixed reason vocabulary. |
| `unknown_commits_by_stage` | `commit_unknown` | Uncertain local commits split by the fixed stage vocabulary. |
| `preparation_duration` | `preparation_finished.duration_ms` | Callback settlement durations for both returned and thrown callbacks. |
| `operation_duration` | `shutdown.elapsed_ms` | Whole-operation durations including cleanup, for every observed outcome. |
| `saturated` | Arithmetic clipping | Sticky indication that at least one counter, bucket or sum exceeded the safe integer range. |

All current labels appear, including zero counts. The label vocabularies and
counting rules match [Node preparation metrics](event-acknowledgment-node-metrics.md),
using Python field names. Python snapshots are detached mutable containers; Node
snapshots are recursively frozen.

These metric families observe different parts of the same lifecycle. Do not add
commit, uncertainty, renewal failure and shutdown counters into one operation
total. A late renewal failure can be counted while the operation remains
committed. A receipt fast path can increment replay and committed-operation
counters without a preparation sample. A returned preparation can still precede
a failed final commit. Local commits do not prove platform callback confirmation
or unique business effects.

## Histograms and saturation

`EVENT_ACKNOWLEDGMENT_PREPARATION_DURATION_BUCKETS_MS` is the immutable tuple of
finite inclusive bounds used by both histograms:

```text
1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000,
10000, 30000, 60000, 300000
```

Each histogram has `buckets`, `count`, `sum_ms` and `saturated`. Each bucket has
`upper_bound_ms` and `count`. Bucket counts are cumulative, counting observations
less than or equal to the bound. The final `upper_bound_ms: None` represents
positive infinity; its count equals histogram `count`. Zero milliseconds enters
the first bucket. Samples retain the event contract's whole milliseconds; they
do not estimate lease validity or remaining budgets.

Counts and sums clip at `9007199254740991` only when an addition would exceed it.
This matches Node's safe integer limit and preserves integer interoperability.
Each histogram has its own sticky saturation flag; the top-level flag also covers
ordinary counters. Affected values become lower bounds on accepted observations.
Collection continues without wrapping or resetting. Avoid derived rates, means
or quantiles from clipped values. A false flag means arithmetic has not clipped;
it makes no claim about event coverage or stream completeness.

## Input validation and execution context

`record` accepts exact current SDK frozen event variants or exact plain `dict`
metadata, such as `dataclasses.asdict(event)`. It rejects subclasses, other
mappings and arbitrary objects without reading their properties. SDK variants
are read through their captured native slot descriptors. Dictionaries must have
only the current required/optional fields and native string keys. No arbitrary
getter, mapping adapter or value coercion is invoked.

Kinds, outcomes, reasons and stages must be current literals. Durations must be
native integers from zero through the safe integer limit; booleans, floats and
integer subclasses are rejected. Replay flags must be native booleans. Shutdown
reason may be absent, `None` or a current reason literal. Missing/extra fields or
invalid values raise a generic `ValueError` without forwarding a validation cause
or raw rejected data. Complete validation precedes any mutation. Structural
validation does not establish event ordering or correlate an operation's history.

The worker's existing observer boundary swallows observer failures. A direct
invalid `record` call raises before updating that call's metrics. Keep event and
collector versions compatible. No exporter or arbitrary adapter should run inside
the observer.

Serialize `record` and `snapshot` calls in one execution context. Both methods
are synchronous, without awaits: operations sharing one asyncio event-loop thread
can share the collector. There is no internal lock or thread affinity check.
If other threads read or write it, the host must serialize all accesses, including
snapshots, and avoid blocking the processing loop.

## Export outside the observer

For Prometheus text 0.0.4, use the pure
[`format_event_acknowledgment_preparation_metrics`](event-acknowledgment-python-prometheus.md)
formatter. It handles fixed labels, cumulative buckets, seconds and saturation
without registering metrics or opening a listener.

Read snapshots from an application-owned export path. Publish cumulative bucket
counts, `count` and `sum_ms` as absolute observations. For Prometheus-style seconds,
map `None` to `+Inf` and divide finite bounds and `sum_ms` by 1000. Choose a fixed
namespace and fixed label mappings. Do not increment an exporter counter by the
whole cumulative total on each poll; that would count old observations again.

An OpenTelemetry adapter must handle its histogram aggregation and temporality,
converting cumulative to per-bucket counts and managing deltas/resets as required.
Do not fabricate individual samples from means or feed snapshots back as events.
The collector includes no exporter, automatic delta conversion or network
publication.

Collection is process-local and cumulative from construction. Duplicate events
count again. There are no IDs, sequence tracking, deduplication, active-work gauges,
reset/restore/merge APIs or durable checkpoints. Constructing a new collector
starts at zero; exporters own process identity and reset detection. Dropped
observers, filtered events, crashes and unfinished cleanup can omit observations.
Subtracting start/shutdown counts does not establish in-flight work. Use retained
[recovery health](event-acknowledgment-intent-health.md#python-summary) and
[processing inspection](event-consumer-processing-inspection.md#python-inspection)
separately for recovery evidence.

The [protected-job example](../examples/event-acknowledgment-python/README.md#collect-preparation-metrics)
wires this observer into existing version-5 processing. Local verification covers
compilation/lint, Python 3.11 syntax, isolated generation, package inclusion and
static contract/documentation checks. No tests or assertions are added or changed
and no local tests are run. Hosted acceptance for validation, cumulative buckets,
saturation, detached snapshots and lifecycle counting remains pending before
release.
