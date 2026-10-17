# Python preparation metrics in Prometheus

`format_event_acknowledgment_preparation_metrics` renders a complete
[preparation collector snapshot](event-acknowledgment-python-metrics.md) as
Prometheus text 0.0.4. Read the same long-lived collector that receives prepared
worker events, from a separate scrape path:

```python
from faas_sdk import (
    EVENT_ACKNOWLEDGMENT_PREPARATION_PROMETHEUS_CONTENT_TYPE,
    EventAcknowledgmentPreparationMetrics,
    format_event_acknowledgment_preparation_metrics,
)

metrics = EventAcknowledgmentPreparationMetrics()
# Pass on_event=metrics.record to prepared operations.

body = format_event_acknowledgment_preparation_metrics(metrics.snapshot())
content_type = EVENT_ACKNOWLEDGMENT_PREPARATION_PROMETHEUS_CONTENT_TYPE
```

The pure formatter returns a string without registering metrics, retaining
snapshots, changing processing, opening a listener or starting tasks/timers. It
has no metrics dependency. The content type is
`text/plain; version=0.0.4; charset=utf-8`. Output includes HELP/TYPE metadata,
zero-valued series and a final newline, without timestamps or an OpenMetrics EOF
marker. Generate the complete body before sending success headers.

## Names and labels

The default prefix is `gregale_event_acknowledgment_preparation`. An underscore
separates it from each suffix below. Names, HELP text and label mappings match
[Node exposition](event-acknowledgment-node-prometheus.md):

| Suffix | Type | Fixed labels | Source |
| --- | --- | --- | --- |
| `events_total` | counter | `kind` | Every accepted lifecycle event. |
| `operations_total` | counter | `outcome` | Shutdown observations only. |
| `callbacks_total` | counter | `outcome="returned"` or `"threw"` | Preparation callback settlements. |
| `local_commits_total` | counter | `receipt="new"` or `"replayed"` | Confirmed local receipt/intent commits. |
| `renewal_failures_total` | counter | `lease="local"` or `"platform"`, `reason` | Observed renewal failures. |
| `unknown_commits_total` | counter | `stage` | Uncertain local commit observations. |
| `callback_duration_seconds` | histogram | `le` on buckets | Preparation callback settlement durations. |
| `operation_duration_seconds` | histogram | `le` on buckets | Whole-operation durations through cleanup. |
| `saturated` | gauge | `scope="collector"`, `"callback_duration"` or `"operation_duration"` | Sticky clipping flags, 0 or 1. |

The collector's fixed event/outcome/reason/stage vocabularies determine all label
values. The current contract emits nine families and 92 series per prefix,
including zeroes. No identity, token, job, payload or arbitrary reason labels are
added. Configure stable target/process labels in the monitoring system.

Use the keyword-only `prefix` for an application-owned namespace:

```python
body = format_event_acknowledgment_preparation_metrics(
    metrics.snapshot(), prefix="billing_event_preparation",
)
```

A prefix must be a native string of 1..128 ASCII letters, digits or underscores,
beginning with a letter or underscore. The default applies when omitted; `None`
is invalid. Use stable trusted application configuration. Incoming deliveries
must not determine metric names. The function accepts no arbitrary labels/options.

## Counters, histograms and precision

The formatter preserves absolute cumulative observations. It never increments
another registry or converts to deltas. Repeated rendering of the same values
produces the same text regardless of dictionary insertion order. Read a fresh
snapshot per scrape and keep target identity distinct across worker processes.
A new collector starts at zero; monitoring owns reset detection and aggregation.
Use distinct prefixes when combining independent collectors on one endpoint.

Both histograms expose `_bucket`, `_sum` and `_count`. Finite bounds and sums
convert from milliseconds to seconds using integer decimal formatting, without
an intermediate floating-point conversion. Bounds are:

```text
0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5,
1, 2.5, 5, 10, 30, 60, 300, +Inf
```

Buckets remain cumulative and inclusive. The final source `None` bound becomes
`le="+Inf"`, and its count equals `_count`. Downstream Prometheus numbers still
use floating point; exact decimal text adds no measurement precision to the
whole-millisecond source. No synthetic samples, rates, means or quantiles are
computed.

Saturation gauges preserve the collector and per-histogram flags. Clipped values
are lower bounds; avoid derived rates/means/quantiles while clipped. A zero flag
means no arithmetic clipping, without establishing observation coverage. Local
commits do not prove callback confirmation or unique business effects. Do not
sum metric families into one operation total or infer active work by subtracting
shutdowns from starts. Recovery uses retained
[intent health](event-acknowledgment-intent-health.md#python-summary) and
[processing inspection](event-consumer-processing-inspection.md#python-inspection).

## Snapshot validation and execution context

The formatter accepts the current collector's native dictionaries/lists, including
JSON round trips. Every dictionary must have exactly the required native string
keys; subclasses, arbitrary mappings/objects and non-native container types are
rejected without reading their properties. No getter, coercion or adapter is
called. All counters, bucket counts, bounds and millisecond sums must be native
integers within `0..9007199254740991`; bool/float/integer subclasses are invalid.
Flags must be native booleans.

Both histograms must have all 16 exact bounds in order, nondecreasing counts and
matching infinity/total counts. A zero total requires a zero sum. A histogram
saturation flag requires collector saturation. Snapshot fields, every zero label
and every bucket are required; unknown keys are rejected. Validation checks
structure and these invariants, without authenticating observations or proving
lifecycle history.

Complete validation and copying precede rendering. Invalid snapshots or prefixes
raise a fixed `ValueError` without forwarding rejected data or a validation cause;
no partial text is returned. Do not mutate snapshots concurrently with formatting.
Serialize collector reads/writes in one execution context as required by the
collector. The formatter performs no reset, recording or recovery action.

## Application-owned scrape path

The [ASGI scrape example](../examples/event-acknowledgment-python/README.md#expose-preparation-metrics-to-prometheus)
exports an explicit application factory for the same collector and event-loop
thread as processing. It starts no listener on import. Only `GET /metrics` without
a query is served; other paths/queries return 404 and other methods on that path
return 405. Snapshot/format validation errors produce a generic 503 before success
headers; host cancellation and unexpected host failures propagate. All responses
disable caching.

The host supplies its ASGI server, loopback binding, listener failure handling and
shutdown. Use one process with the same in-memory collector, disable lifespan for
this HTTP-only app and keep the listener separate from delivery ingress. Remote
scrapes use the host's authenticated proxy/protected transport. No authentication
middleware or monitoring deployment is installed by this helper.

Local verification covers compilation/lint, Python 3.11 syntax, isolated SDK
generation, wheel/sdist inclusion, static family/vocabulary/source comparisons
and schema/ADR/documentation checks. No tests or assertions are added or changed
and no local tests are run. Hosted acceptance for Prometheus parsing, decimal/
infinity conversion, malformed inputs, cumulative buckets, repeated rendering
and scrape responses remains pending before release.
