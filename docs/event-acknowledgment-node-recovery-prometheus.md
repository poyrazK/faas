# Node acknowledgment recovery-health metrics

For actual pass timing independent of database reads, see
[Node worker liveness](event-acknowledgment-node-worker-liveness.md).

`EventAcknowledgmentIntentHealthMetrics` caches one bounded, owner-scoped health
observation. `formatEventAcknowledgmentIntentHealthMetrics` formats a detached
snapshot as Prometheus text 0.0.4. Scraping performs no SQL, callback, recovery or
business work. Construction/import starts no timer or listener.

## Feed completed reads

```js
import {
  EventAcknowledgmentIntentHealthMetrics,
  formatEventAcknowledgmentIntentHealthMetrics,
  getEventAcknowledgmentIntentHealth,
} from '@gregale/sdk-node/preview';

const metrics = new EventAcknowledgmentIntentHealthMetrics(accountId, appId, {
  maxAgeMs: 30000,
});
// The host schedules sequential, bounded reads and joins acquisition/query/release.
try {
  const health = await getEventAcknowledgmentIntentHealth(pool, accountId, appId, {
    scanLimit: 1000, stallAfterMs: 300000, signal: querySignal,
  });
  metrics.record(health);
} catch {
  metrics.recordReadFailure();
}
// Recompute at each scrape; never cache the formatted text or snapshot indefinitely.
const body = formatEventAcknowledgmentIntentHealthMetrics(metrics.snapshot());
```

The constructor binds normalized trusted account/app UUIDs, without exporting
them. `maxAgeMs` is a whole number from 100 through 300000, default 30000. Set it
to cover the host's poll/read budgets; the example polls every five seconds after
completion with a five-second cooperative query deadline. Bound actual driver
acquisition, SQL and release calls because cancellation does not interrupt an
unresponsive driver. Wait for each read and cleanup before polling again.

`record(health)` copies only selected own primitive metadata. It checks scope,
coverage kind, bounded integer counts, completeness and count relationships.
Selected getters, malformed input and foreign scope throw a generic `TypeError`
and clear the cached sample. `recordReadFailure()` likewise clears it. Neither
retains raw errors, callbacks, capabilities, payloads or invocation identity.
Snapshots are detached/frozen. Memory is constant: one sample, one database
timestamp high-water mark and fixed configuration.

## Evidence and missing data

Recording a valid read sets `readSuccess`, including incomplete/stale evidence.
Eligibility additionally requires complete coverage, a strictly advancing database
timestamp, no future observation and age within `maxAgeMs`. Fresh incomplete
observations advance the high-water mark. Failure retains that mark so an old
sample cannot become fresh through a failed-read reset. Repeated/regressing
timestamps invalidate eligibility; they never extend freshness.

Each `snapshot()` recomputes age as the greater of wall-clock database age and
age-at-record plus monotonic elapsed time. Wall-clock regression relative to
recording also invalidates eligibility. A future/stale/non-advancing sample
cannot become eligible just by waiting. No sustained confirmation or incident
identity is reconstructed by this cache; Prometheus alert hold times are separate.

Before the first valid read, after failure or after malformed recording,
`health` is absent and both status flags are false. Counts, coverage and ages
are omitted from exposition. Incomplete/stale valid reads retain sampled counts
as historical/lower-bound data with `evidenceEligible=false`. Counts never become
fabricated zeroes on failure. Complete zero counts describe only retained work
in the selected application database and scope, not callback/business success.

## Exposition contract

Default prefix: `gregale_event_acknowledgment_recovery`. All families are gauges;
there are no SDK-generated labels or explicit sample timestamps. The optional
formatter `{ prefix }` must be an ASCII identifier, 1..128 characters, starting
with a letter or underscore and containing only letters/digits/underscores.
Malformed snapshots/options fail before any text is returned. Use
`EVENT_ACKNOWLEDGMENT_INTENT_HEALTH_PROMETHEUS_CONTENT_TYPE` for HTTP responses.

| Suffix | Meaning |
| --- | --- |
| `read_success` | Latest recording supplied valid scoped metadata; zero before a read/after failure. |
| `evidence_eligible` | Fresh complete advancing evidence remains eligible at snapshot time. |
| `max_observation_age_seconds` | Configured age bound; always present. |
| `complete`, `truncated`, `scan_limit`, `invalid_rows` | Coverage of the latest valid bounded sample; omitted without a sample. |
| `outstanding`, `due`, `stalled`, `parked` | Sampled retained counts; omitted without a sample. |
| `observation_timestamp_seconds` | Database observation time in Unix seconds; omitted without a sample. |
| `observation_age_seconds` | Conservative cached observation age; omitted without a sample. |

Use one collector per owner scope and one scrape target per collector. Bind
separate scopes to separate targets. Maintain authorized scope mapping in host
configuration; never add account, invocation, token or payload values as labels.
Different targets may observe the same database scope, so summing them does not
produce a global backlog. Target/discovery labels are application configuration.

The [explicit host example](../examples/event-acknowledgment-node/recovery-metrics.mjs)
keeps SQL out of the scrape handler, clears evidence when polling stops and joins
active reads before shutdown. The [monitoring pack](../examples/event-acknowledgment-monitoring/README.md#recovery-health-node)
provides five recording rules, six alerts and a separate dashboard/runbook.
Its rules also check scrape reachability and database timestamps against
Prometheus time, so a repeatedly scraped frozen snapshot cannot remain healthy.
Scrape/evaluation intervals still introduce observation delay; synchronize clocks.

Prometheus backlog alerts can stop matching when evidence disappears. That
resolution is a notification lifecycle event, not proof of recovery. Inspect the
evidence/down/missing-target alerts. For retained incident identity across missing
evidence/restarts, use the [checkpointed watch](event-acknowledgment-node-checkpoints.md).

Local evidence is generation, build, package, syntax and static rule/documentation
checks. Hosted qualification remains pending for clocks, stale/repeated evidence,
driver cancellation, live scraping, alert transitions and Grafana rendering.

Python provides [matching recovery gauges](event-acknowledgment-python-recovery-prometheus.md)
with its own same-loop ASGI host and a distinct scrape job. Both use the shared
rules/dashboard and fixed metric prefix; typed SDK snapshots remain independent.

Go provides a [concurrent cache, explicit poller and net/http adapter](event-acknowledgment-go-recovery-prometheus.md)
with the same contract and a distinct loopback 9468 target.
