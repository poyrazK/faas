# Node preparation metrics in Prometheus

Python exposes the same metric names, labels and histogram contract through
[Python Prometheus exposition](event-acknowledgment-python-prometheus.md).

`formatEventAcknowledgmentPreparationMetrics` renders a complete
[preparation collector snapshot](event-acknowledgment-node-metrics.md) as
Prometheus text 0.0.4. Read from the same long-lived collector that receives
prepared-worker events:

```js
import {
  EventAcknowledgmentPreparationMetrics,
  formatEventAcknowledgmentPreparationMetrics,
  EVENT_ACKNOWLEDGMENT_PREPARATION_PROMETHEUS_CONTENT_TYPE,
} from '@gregale/sdk-node/preview';

const metrics = new EventAcknowledgmentPreparationMetrics();
// Pass onEvent: metrics.record to your prepared operations.

// In your independent scrape handler:
const body = formatEventAcknowledgmentPreparationMetrics(metrics.snapshot());
res.writeHead(200, {
  'content-type': EVENT_ACKNOWLEDGMENT_PREPARATION_PROMETHEUS_CONTENT_TYPE,
  'cache-control': 'no-store',
});
res.end(body);
```

The formatter returns a string and has no side effects. It does not register
metrics, open a listener, start a timer, retain snapshots or change processing.
The content type is `text/plain; version=0.0.4; charset=utf-8`. Output includes
HELP/TYPE metadata, all zero-valued series and a final newline. It contains no
timestamps or OpenMetrics EOF marker; serve it with the exported content type.

## Names and labels

Every name begins with `gregale_event_acknowledgment_preparation_`. The table
shows the suffix after that prefix:

| Suffix | Type | Fixed labels | Source |
| --- | --- | --- | --- |
| `events_total` | counter | `kind` | Every accepted lifecycle event. |
| `operations_total` | counter | `outcome` | Shutdown outcomes only. |
| `callbacks_total` | counter | `outcome="returned"` or `"threw"` | Preparation callback settlements. |
| `local_commits_total` | counter | `receipt="new"` or `"replayed"` | Confirmed local receipt/intent commits. |
| `renewal_failures_total` | counter | `lease="local"` or `"platform"`, `reason` | Observed renewal failures. |
| `unknown_commits_total` | counter | `stage` | Uncertain local commit observations. |
| `callback_duration_seconds` | histogram | `le` on buckets | Preparation callback settlement durations. |
| `operation_duration_seconds` | histogram | `le` on buckets | Operation durations through shutdown cleanup. |
| `saturated` | gauge | `scope="collector"`, `"callback_duration"` or `"operation_duration"` | Sticky safe-integer clipping flags, 0 or 1. |

Kinds, outcomes, reasons and stages use the collector's
[closed lifecycle vocabulary](event-acknowledgment-node-telemetry.md). Zero-valued
combinations are included. The current contract emits nine families and 92 series
per prefix. No app, subscription, event, job, token, arbitrary reason or payload
labels are added. Configure fixed target labels in your monitoring system.

For an application-owned namespace, pass a stable prefix:

```js
const body = formatEventAcknowledgmentPreparationMetrics(metrics.snapshot(), {
  prefix: 'billing_event_preparation',
});
// Example name: billing_event_preparation_operations_total
```

Prefixes must have 1..128 ASCII characters, begin with a letter or underscore,
and contain only letters, digits and underscores. One underscore is appended
before each suffix. Omitted/undefined prefixes use the default; null is invalid.
Use trusted application configuration, keep it stable across scrapes, and do not
derive it from incoming deliveries. Unknown options and extra labels are rejected.

## Histogram and counter semantics

The formatter preserves absolute cumulative counts. It never increments another
registry counter or converts to deltas. Repeatedly formatting the same snapshot
returns the same text, independent of object key insertion order. Read a fresh
snapshot for each scrape; a new collector starts at zero. Keep target/process
identity distinct across worker replicas and let Prometheus handle counter resets.
Avoid duplicate family/sample names when combining text with other exporters or
collectors; use distinct prefixes or aggregate deliberately outside this helper.

Both classic histograms expose `_bucket`, `_sum` and `_count`. Finite bucket
bounds and sums are divided by 1000, converting milliseconds to seconds. Bounds
are `0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60,
300, +Inf`. Buckets stay cumulative and inclusive; the final null source bound
becomes `le="+Inf"` and its count equals `_count`. No synthetic observations,
averages, rates or quantiles are computed. Prometheus numbers use floating point;
conversion to seconds does not add precision to the source millisecond samples.

The collector saturation gauge is 1 if any counter or histogram clipped. Each
histogram scope is 1 only if that histogram clipped. Affected values are lower
bounds; avoid derived rates/averages/quantiles while clipped. A 0 flag says only
that arithmetic did not clip. It does not establish complete event coverage.

Local commits do not confirm platform callback delivery or unique business
effects. Preparation success can precede a failed commit. Do not sum the metric
families into one operation total or derive active work by subtracting shutdowns
from starts. For recovery, use [intent health](event-acknowledgment-node-intents.md)
and [receipt/claim inspection](event-consumer-processing-inspection.md).

## Input validation and scrape ownership

The formatter copies a complete current-contract snapshot before producing text.
Required fields must be own data properties; keys, label maps and array shape must
match the current contract. Counter values, counts and millisecond sums must be
nonnegative safe integers. Histogram bounds must match the collector, bucket
counts must be nondecreasing and the infinity count must equal the total. A zero
count requires a zero sum; histogram saturation requires collector saturation.

Unexpected keys, symbols, accessors, missing fields, invalid options or throwing
descriptor traps raise a generic TypeError without an attached raw cause. Getters
are not invoked. JSON round trips of current snapshots are supported; validation
does not authenticate observations or check lifecycle history. No partial text is
returned on validation failure. Generate the body before sending success headers.

The [loopback scrape example](../examples/event-acknowledgment-node/README.md#expose-preparation-metrics-to-prometheus)
creates an application-owned server only when explicitly called and binds to
`127.0.0.1` in its usage example. It exports the same collector that receives job
events, serves only `GET /metrics`, and returns a generic 503 on collection/format
errors. Keep it separate from delivery ingress. For remote scrapes, use your
existing authenticated proxy or protected transport; the example supplies no
authentication layer. The host owns listener errors and shutdown.

The [shared Node/Python monitoring pack](../examples/event-acknowledgment-monitoring/README.md)
provides guarded recording rules, nine warning alerts and a Grafana dashboard.
It retains per-worker scope, leaves unavailable/low-volume derived series
missing and links uncertain commits to protected receipt/intent inspection.
Installing the SDK or importing the example starts no monitoring deployment.

Validation covers compilation, generation, packaging, example syntax and static
checks. No tests or assertions were added or changed and no local tests were run.
Hosted acceptance remains required for text parsing, unit/infinity conversion,
bucket/count consistency, labels, repeat rendering, malformed inputs and scrape
responses before release.
