# Go acknowledgment recovery-health metrics

`NewEventAcknowledgmentIntentHealthMetrics` constructs a bounded owner-scoped
cache. An explicit poller feeds it, and a cache-only `net/http` handler exposes
the same thirteen gauges as [Node](event-acknowledgment-node-recovery-prometheus.md)
and [Python](event-acknowledgment-python-recovery-prometheus.md). Scraping performs
no SQL, callback or recovery work. Construction starts no goroutine or listener.

## Poll and serve explicitly

```go
metrics, err := faas.NewEventAcknowledgmentIntentHealthMetrics(accountID, appID,
    faas.EventAcknowledgmentIntentHealthMetricsOptions{MaxAge: 30 * time.Second})
if err != nil {
    return err
}
handler, err := faas.NewEventAcknowledgmentIntentHealthMetricsHandler(metrics,
    faas.EventAcknowledgmentIntentHealthPrometheusOptions{})
if err != nil {
    return err
}
// Give handler to a separately supervised, protected HTTP server.
// Run the following in the host's polling goroutine and join it on shutdown:
return faas.WatchEventAcknowledgmentIntentHealthMetrics(ctx, db, accountID, appID,
    metrics, faas.EventAcknowledgmentIntentHealthMetricsWatchOptions{
        Health: faas.EventAcknowledgmentIntentHealthOptions{
            ScanLimit: 1000, StallAfter: 5 * time.Minute,
        },
        PollInterval: 5 * time.Second,
        QueryTimeout: 5 * time.Second,
    })
```

Imports are `context`/`database/sql` as used by the host, `time`, and the SDK as
`faas`. The host supplies trusted account/app UUIDs and a configured PostgreSQL
`*sql.DB`; no platform client or credentials are needed for monitoring. Install
the current [application-owned intent schema](event-acknowledgment-intents.md)
before using these reads. The poller reuses the existing bounded
[health SELECT](event-acknowledgment-intent-health.md); it changes no rows/schema.

Zero-valued options select defaults: scan 1000, stall five minutes, polling/query
five seconds each, maximum observation age thirty seconds. `MaxAge` accepts whole
milliseconds 100..300000; poll intervals 100..60000 and query deadlines 10..30000.
Health scan bounds are 1..10000 and stall bounds one second..24 hours. Choose the
age budget to cover polling, query and scheduling delays.

Only one poller can own a cache, and its normalized scope must match. Invalid
options, nil context/pool, a foreign scope or concurrent poller fail before SQL.
Reads run synchronously without helper goroutines or abandoned work. The next
read follows the completed read and poll delay. Each read's cooperative context
deadline covers pool acquisition and `Scan`; expired contexts and late returns
clear evidence even when a driver returns a nominally successful result.

Cancellation waits for the active database call to return, clears cached sample
data, then returns the caller's context error. The application must bound driver
connect, statement, release and cancellation operations; context cancellation
cannot force an unresponsive driver to finish. Keep the pool open until polling
has returned. Read failures clear evidence and polling continues; raw driver
errors are never cached or exposed as metrics.

## Concurrency and evidence

Use a pointer to the constructed cache; the zero value is invalid and the cache
must not be copied. Mutex-protected state permits concurrent scrapes and polling.
Snapshotting copies a small fixed sample and releases the lock before formatting
or HTTP writes. Each returned sample is independently owned; mutating it cannot
change the cache. Do not mutate a snapshot while another goroutine formats it.

Alternatively, feed sequential completed reads with `metrics.Record(health)`
and `metrics.RecordReadFailure()`. Use one source per cache; do not mix manual
recording with a running poller. Recording validates scope, coverage, bounded
counts, completeness and count relationships, then copies only selected scalar
metadata and a UTC database timestamp. Malformed recording clears the sample and
returns `ErrInvalidEventConsumerRequest`. No owner/invocation identity, token,
callback, payload or arbitrary error survives in a snapshot.

Eligibility requires a complete, fresh, strictly advancing observation when
recorded, and freshness at snapshot time. Fresh incomplete reads also advance the
database high-water mark; failure and poller shutdown retain it. Repeated,
regressing, future or stale evidence cannot become eligible just through waiting.
Snapshot age is the greater of wall/database age and initial age plus Go's local
monotonic elapsed time. Wall-clock regression relative to recording invalidates
eligibility. Nanosecond duration comparisons saturate beyond Go's interval range,
while raw age uses second arithmetic across all accepted years. Timestamp
exposition preserves fractional seconds without `UnixNano` overflow.

Before the first valid read, after failure or after stopping, `Health == nil`,
`ReadSuccess == false` and `EvidenceEligible == false`. Sample gauges are omitted,
never zero-filled. Valid incomplete/stale samples retain raw bounded counts with
ineligible evidence. This measures retained metadata coverage, not business
success, callback confirmation or recovery-host availability.

## Exposition and host ownership

`FormatEventAcknowledgmentIntentHealthMetrics(metrics.Snapshot(), options)` is a
pure text-0.0.4 formatter. Names, HELP strings, emission order and content type
match the [shared gauge contract](event-acknowledgment-node-recovery-prometheus.md#exposition-contract).
All thirteen families are gauges without SDK-generated labels or explicit sample
timestamps. Three status/configuration gauges always appear; the other ten need
a valid cached sample. Malformed snapshots/options return empty text and the
fixed request error. Nil/zero caches yield an invalid zero snapshot rejected by
the formatter; failure recording on such a cache is a no-op.

The optional `Prefix` uses `gregale_event_acknowledgment_recovery` when empty.
Other values must be ASCII identifiers 1..128 bytes, starting with a letter or
underscore and containing only letters, digits or underscores. Custom prefixes
require matching rule/dashboard changes. Use
`EventAcknowledgmentIntentHealthPrometheusContentType` when serving text directly.
Recompute a snapshot on each scrape; never retain formatted evidence indefinitely.

`NewEventAcknowledgmentIntentHealthMetricsHandler` validates the cache/options
before returning an unbound handler. It accepts `GET /metrics` with no query,
returns 404 for other paths/queries and 405 with `Allow: GET` for other methods.
It renders the whole body before writing 200, uses `Cache-Control: no-store` and
generic 503 text for invalid exposition. It holds no database reference.

The [buildable Go host](../sdk/go/examples/event-acknowledgment-recovery/README.md#export-recovery-health-to-prometheus)
uses a separately started server on loopback 9468 with bounded HTTP timeouts.
The host owns supervision, listener/network authorization, graceful shutdown and
database deadlines. Cancel polling, shut down HTTP with a separate bounded
context, join both loops and only then close the pool. Constructors start neither.

Reuse the [Go scrape configuration](../examples/event-acknowledgment-monitoring/prometheus-recovery-go.yml)
and [shared rules/dashboard/runbook](../examples/event-acknowledgment-monitoring/README.md#recovery-health-go).
Use one target per owner scope/cache with distinct target identities; duplicate
observers cannot be summed into global backlog. Keep authorized scope mapping in
host configuration, outside labels. Prometheus independently checks scrape
reachability and observation age against its clock; synchronize clocks and allow
for scrape/evaluation delays.

A backlog alert can resolve when evidence disappears; that does not establish
recovery. Inspect unavailable/down/absence alerts and authoritative rows. Use the
[Go checkpointed watch](event-acknowledgment-go-checkpoints.md) when incident
identity must survive evidence loss/restart. A budget grant or supersession can
lower parked count without confirming a callback. Alerts perform no mutation.

Local builds, vet and static source/monitoring/documentation checks do not qualify
runtime behavior. Hosted qualification remains pending for concurrent recording/
scraping, clocks, stale/repeated evidence, database cancellation/shutdown, HTTP
scraping, alert transitions and Grafana rendering before production use.

For actual recovery-worker availability, use the separate
[Go worker-liveness cache and monitored pass](event-acknowledgment-go-worker-liveness.md).
It adds explicit start/return timing and bounded pass outcomes without allowing
health polling or scraping to refresh worker activity. Its combined handler and
separate opt-in job retain this original health-only contract.
