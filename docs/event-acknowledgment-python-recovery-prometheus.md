# Python acknowledgment recovery-health metrics

`EventAcknowledgmentIntentHealthMetrics` caches one bounded owner-scoped health
sample. The pure `format_event_acknowledgment_intent_health_metrics` formatter
exposes the same thirteen gauge families, prefix, HELP text and fixed no-label
contract as [Node](event-acknowledgment-node-recovery-prometheus.md). Python uses
frozen/slotted options, samples and snapshots with snake_case fields. No database
query, timer, task, listener or callback starts on import or cache construction.

## Record or poll explicitly

```python
from faas_sdk import (
    EventAcknowledgmentIntentHealthMetrics,
    EventAcknowledgmentIntentHealthMetricsOptions,
    awatch_event_acknowledgment_intent_health_metrics,
    format_event_acknowledgment_intent_health_metrics,
)

metrics = EventAcknowledgmentIntentHealthMetrics(
    account_id, app_id, EventAcknowledgmentIntentHealthMetricsOptions(max_age_ms=30000),
)
# The host runs this alongside its ASGI server on the same loop/process.
await awatch_event_acknowledgment_intent_health_metrics(
    lambda: application_pool.connection(timeout=3), account_id, app_id, metrics,
    poll_interval_ms=5000, query_timeout_ms=5000, stop_event=shutdown,
)
# A separately supervised scrape callback formats a new snapshot on every request:
body = format_event_acknowledgment_intent_health_metrics(metrics.snapshot())
```

The connection factory is synchronous and returns a fresh async context manager
per read. It must lease an exclusive idle autocommit psycopg async connection,
discard failed/closed connections and bound acquisition, SQL and cleanup. Defaults
are a 1000-row health scan and five-minute stall interval. Override them with
`health=EventAcknowledgmentIntentHealthOptions(...)` using the
[health bounds](event-acknowledgment-intent-health.md#python-summary).

Poll intervals accept native integer milliseconds 100..60000 and query deadlines
10..30000, both default 5000. The next poll waits until the previous acquisition,
query, release and delay finish. Cooperative deadlines include cleanup and check
actual monotonic elapsed time. The poller reuses the ordinary watch's shielded,
joined read/cancellation handling. It neither races an active SQL operation nor
force-cancels a driver call. Event/native cancellation joins owned work, raises
`EventAcknowledgmentWorkerCancelled` and clears the cached sample in `finally`.
Stop polling before closing its pool/loop. Only one poller per cache and matching
normalized owner scope are accepted; invalid inputs fail before I/O.

Applications may instead feed sequential completed reads with `metrics.record(health)`
and `metrics.record_read_failure()`. Exact current health/options/snapshot/sample
dataclass types are required; native integers exclude booleans/subclasses.
Recording validates owner scope, coverage, flags, bounds and count relationships,
then copies immutable metadata. Malformed recording clears the sample and raises
a fixed `ValueError` without an arbitrary cause/input. Snapshots retain no owner,
invocation, capability, payload or raw storage error. Use one serialized event
loop, or explicitly serialize all access from other threads.

## Freshness and exposition

`max_age_ms` is a native integer 100..300000, default 30000. Choose it to cover
poll/query budgets. Internal wall/database instants and monotonic durations use
integer nanoseconds; canonical UTC observation strings retain six fractional
digits. Unix timestamp exposition preserves those microseconds in decimal text.
Snapshot ages and the configured bound are finite seconds.

Eligibility requires complete, fresh, strictly advancing evidence at recording,
then continued freshness at snapshot time. Fresh incomplete reads also advance
the high-water mark. Failed reads clear counts while retaining that mark.
Repeated/regressing/future/stale samples cannot become eligible just by waiting.
Age is the greater of wall/database age and initial age plus monotonic elapsed
time; wall-clock regression relative to recording also invalidates eligibility.

Before a valid read or after failure, `health=None`, `read_success=False` and
`evidence_eligible=False`. All sample gauges are omitted. Valid incomplete/stale
samples retain bounded raw counts with ineligible evidence. Missing data never
becomes a fabricated zero. This is retained database metadata coverage, not a
business-success or callback-confirmation claim.

The default prefix is `gregale_event_acknowledgment_recovery`; all names/suffixes
match the [Node gauge table](event-acknowledgment-node-recovery-prometheus.md#exposition-contract).
The formatter's optional keyword `prefix` is a native ASCII string 1..128
characters, beginning with a letter/underscore and containing only letters,
digits and underscores. Malformed snapshots/options fail before returning text.
Use `EVENT_ACKNOWLEDGMENT_INTENT_HEALTH_PROMETHEUS_CONTENT_TYPE` for HTTP responses.
No SDK-generated labels or explicit sample timestamps are emitted. Never cache
formatted text/snapshots indefinitely; recompute each scrape.

The [same-loop ASGI example](../examples/event-acknowledgment-python/README.md#export-recovery-health-to-prometheus)
and [Python scrape configuration](../examples/event-acknowledgment-monitoring/prometheus-recovery-python.yml)
reuse the shared recovery rules/dashboard. Use unique target identities and one
scrape job per collector; duplicate observers cannot be summed into a global
backlog. Keep authorized owner mapping in host configuration, outside labels.
Mixed hosts use distinct ports, with one installed rule group.

Prometheus also checks reachability and observation age against its clock, with
normal scrape/evaluation delay. A backlog alert can stop matching when evidence
is lost; its resolution does not prove recovery. Inspect unavailable/down/absence
alerts and authoritative rows. Use the
[checkpointed Python watch](event-acknowledgment-python-checkpoints.md) when stable
incident identity must survive unknown evidence/restart. A budget grant or newer
staging can lower parked count without confirming a callback.

Local verification is syntax, lint, generation, packaging and static monitoring/
documentation checks. Hosted qualification remains pending for clock changes,
driver cancellation, live ASGI scraping, alert transitions and Grafana rendering.

Go provides a [concurrent cache, explicit poller and net/http adapter](event-acknowledgment-go-recovery-prometheus.md)
with the same contract and a distinct loopback 9468 target.
