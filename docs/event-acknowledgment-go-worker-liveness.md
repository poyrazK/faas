# Go recovery-worker liveness

Recovery-health queries describe retained database backlog. They cannot prove
that a recovery worker is running. `EventAcknowledgmentIntentRecoveryWorkerMetrics`
observes actual bounded recovery passes independently from health reads and
scraping. A combined cache-only HTTP adapter exposes both contracts.

## Observe actual passes

```go
metrics, err := preview.NewEventAcknowledgmentIntentRecoveryWorkerMetrics(accountID, appID,
    preview.EventAcknowledgmentIntentRecoveryWorkerMetricsOptions{
        MaxIdle: time.Minute,
        MaxPassDuration: 5 * time.Minute,
    })
if err != nil {
    return err
}
// Called explicitly by the application's scheduler, using its existing resources:
progress, err := preview.RecoverEventAcknowledgmentIntentsWithMetrics(ctx, db, client,
    accountID, appID, preview.EventAcknowledgmentIntentRecoveryPassOptions{
        MaxAttempts: 100, Concurrency: 4,
    }, metrics)
// Inspect BOTH original progress and err before deciding whether to schedule again.
```

Import the SDK as `faas` and `time` for these options. The wrapper uses the
[existing recovery pass](event-acknowledgment-go-recovery-pass.md), preserving its
scalar result, joined error categories, claim/budget fences and cancellation.
It records a start immediately before invoking that pass and a completion only
after every admitted worker has joined. It adds no scheduler, callback retry,
background task or heartbeat timer. SQL, platform callbacks and recovery happen
only through the explicitly invoked pass, never through metrics scraping.

The constructor normalizes trusted account/app UUIDs and begins observing the
first-pass idle gap. `MaxIdle` is a whole-millisecond duration 100ms..24h, default
one minute. `MaxPassDuration` is 1s..24h in whole milliseconds, default five minutes.
Zero selects the default. These are observation expectations, not deadlines;
configure actual database/HTTP operations and the caller's context separately.

Use one cache per owner scope and nonoverlapping scheduled pass stream. Nil/zero
caches, invalid pass options, foreign scope and overlapping passes reject before
recovery I/O. A pre-canceled valid invocation returns interruption without
refreshing activity. Do not copy a cache. Concurrent scrapes are synchronized;
returned snapshots/results are independent copies, containing no IDs, protected
callback data, clients, contexts or arbitrary errors. Do not mutate a snapshot
while another goroutine formats it.

## Activity and outcomes

The reference clock starts at construction. Actual pass start and joined return
are the only subsequent activity updates. While idle, age measures the gap since
construction or latest return and is compared with `MaxIdle`. While a pass is in
flight, it measures time since that pass started and uses `MaxPassDuration`.
Scrapes, independent health polling and unmonitored passes cannot refresh it.

Ages use the greater of wall-clock age and local monotonic elapsed time. Detected
wall/monotonic regression permanently invalidates that cache's liveness evidence;
review/correct the clock before explicitly replacing it. Unknown clock evidence
is never interpreted as a healthy worker. Time passing cannot manufacture pass
completion. A panic leaves the tracked pass in flight; investigate the original
work and do not replace the cache simply to bypass overlap protection.

Last-start time updates on admission to a monitored pass. Last-return time,
duration and scalar progress remain absent until an actual return. They retain
the previous joined pass while another is active. Duration is local monotonic
elapsed time, and completion includes failed/interrupted/unknown-COMMIT returns;
it does not mean the pass or callbacks succeeded. Malformed returned progress
clears outcome evidence rather than replacing it with zeros; timing remains known.

All outcome counts are **latest-pass gauges**, not counters or database backlog.
Never apply `rate()` or sum successive samples. Attempts count admissions,
including empty probes; exactly one outcome is recorded per admission, bounded
by 10000. Confirmed, unconfirmed, parked, superseded, empty, claim-lost,
interrupted, failed and unknown-COMMIT outcomes stay separate. Unknown COMMIT
retains the original recovery/error precedence. A known parking outcome is not
total retained parked work. An empty/limit stop does not prove global drain or
business success. Continue inspecting the original progress/error in the host.

## Metrics and HTTP

`FormatEventAcknowledgmentIntentRecoveryWorkerMetrics(metrics.Snapshot(), options)`
is pure text 0.0.4. The default prefix is
`gregale_event_acknowledgment_recovery_worker`; a custom `Prefix` must follow the
[health identifier bounds](event-acknowledgment-go-recovery-prometheus.md#exposition-and-host-ownership).
All 23 fixed families are gauges, without generated labels or explicit sample
timestamps. The existing health content-type constant applies. Malformed
snapshots/options return empty text and a fixed request error.

| Suffixes | Presence and meaning |
| --- | --- |
| `clock_valid`, `overdue`, `in_flight` | Always; timing evidence, expected-bound breach and tracked pass state. |
| `max_idle_seconds`, `max_pass_seconds` | Always; host scheduling/duration expectations. |
| `activity_timestamp_seconds`, `activity_age_seconds` | Always; construction/start/return reference and conservative age. |
| `last_pass_result_valid` | Always; zero before a return or after invalid scalar progress. |
| `last_started_timestamp_seconds` | Only after an actual monitored pass starts. |
| `last_completed_timestamp_seconds`, `last_pass_duration_seconds`, `last_completed_age_seconds` | Only after an actual joined return, regardless of its outcome. |
| `last_pass_success` | Only with valid progress; one for limit/empty stop, not callback/business success. |
| `last_pass_attempts`, `last_pass_confirmed`, `last_pass_unconfirmed`, `last_pass_parked`, `last_pass_superseded`, `last_pass_empty`, `last_pass_claim_lost`, `last_pass_interrupted`, `last_pass_failed`, `last_pass_commit_unknown` | Only with valid bounded progress; latest joined pass only. |

`NewEventAcknowledgmentIntentRecoveryMetricsHandler(health, worker)` requires
matching constructed owner scopes. It combines the original thirteen health
families and worker families under their fixed default prefixes. GET /metrics
with no query returns fully formatted text and no-store headers; other paths/
queries return 404 and other methods 405. Invalid exposition returns generic 503
before a success body is written. Each cache is copied separately; this is not an
atomic database/activity transaction. Scraping starts no SQL or recovery work.

The [buildable combined host](../sdk/go/examples/event-acknowledgment-recovery/README.md#observe-actual-recovery-worker-activity)
keeps the existing loopback 9468 listener and health settings. Explicitly supervise
`RunHealth`, HTTP serving and application scheduling of `RecoverCallbacks`.
Process progress/error before another pass; do not blindly loop after unknown
COMMIT. Cancel/join active recovery and health reads, shut down/join HTTP, then
close database/client resources. Metrics cannot force an unresponsive driver or
callback to finish, and cannot authorize overlap or fence takeover.

## Opt-in monitoring

Use [prometheus-recovery-go-worker.yml](../examples/event-acknowledgment-monitoring/prometheus-recovery-go-worker.yml)
instead of the health-only Go job for the same endpoint. It sets both
`gregale_event_recovery="true"` and `gregale_event_recovery_worker="true"`.
Existing health-only Go/Node/Python jobs remain unchanged. Do not scrape the same
cache under both jobs or sum duplicate observers. Keep authorized owner mapping
in host configuration, outside labels. Configure expected target inventory.

The shared pack adds three worker recordings and three warning alerts, each held
two minutes. Activity evidence requires a live opted-in scrape, valid local
clock, present timing/bounds and a reference no later than Prometheus time.
Recorded age is the greater of the exported conservative age and Prometheus
time since actual activity. A frozen successfully scraped response cannot keep
worker activity within bounds. Overdue compares idle and active-pass bounds
separately, independently from database-read success/coverage. Missing timing
has its own worker-evidence alert. Failed/unknown-COMMIT counts in the latest
valid returned pass have a separate alert; interruption alone does not fire it.

Dashboard version 4 preserves the original nine health panels and adds worker
state, age/bounds, start/return timestamps, last duration and bounded outcomes.
Missing samples remain Unknown and time-series gaps remain gaps. Alert state is
not a durable incident ledger: a subsequent pass, restart, unavailable evidence
or configuration change can stop an alert matching without proving recovery.
Inspect companion alerts and retained authoritative work. See the
[worker runbook](runbooks/GregaleEventRecovery.md#worker-liveness).

Build/vet and static checks do not qualify runtime concurrency, clock changes,
joined cancellation, live scraping or alert transitions. Hosted behavioral
qualification remains pending before release.
[Node worker liveness](event-acknowledgment-node-worker-liveness.md) follows this
contract with an additional native cleanup gauge. Python worker parity remains
pending; existing health metrics are unchanged.
