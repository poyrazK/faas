# Acknowledgment intent recovery health

The Go SDK can inspect the application-owned acknowledgment intent store and watch
for sustained recovery backlog. Install the schema and configure recovery as
explained in [acknowledgment intents](event-acknowledgment-intents.md) first.
These helpers only read local metadata: they do not claim work, send callbacks,
repair rows, or install tables.

```go
health, err := faas.GetEventAcknowledgmentIntentHealth(ctx, db, accountID, appID,
    faas.EventAcknowledgmentIntentHealthOptions{
        ScanLimit: 1000,
        StallAfter: 5 * time.Minute,
    })
if err != nil {
    return err
}
// Export health.Complete alongside counts and ages.
_ = health

return faas.WatchEventAcknowledgmentIntentHealth(ctx, db, accountID, appID,
    faas.EventAcknowledgmentIntentWatchOptions{
        DueThreshold: 100,
        StalledThreshold: 1,
        Sustain: 30 * time.Second,
    }, func(alert faas.EventAcknowledgmentIntentAlert) {
        // Record locally or hand off promptly to your application's alert system.
        // This callback must return promptly and avoid logging capabilities.
    })
```

## Reading the summary

Each owner-scoped read uses one PostgreSQL statement, snapshot, and database clock.
It samples unfinished (`pending` or `unconfirmed`) rows ordered by next attempt,
invocation, generation, and attempt. Terminal history cannot crowd out unfinished
work. Defaults are 1,000 rows and a five-minute stall interval; accepted ranges are
1–10,000 rows and one second–24 hours.

- `Outstanding`, `Pending`, and `Unconfirmed` describe retained unfinished intents.
- `Leased` counts live recovery claims. `Due` counts eligible work without a live
  claim. `Scheduled` counts future eligibility without a live claim.
- Eligibility begins at the latest of creation, next attempt, and claim expiry.
  `Stalled` counts work eligible for at least `StallAfter`, excluding live claims.
  An old intent waiting on a future retry or a live lease is not stalled.
- `ExpiredClaims` counts expired claims and can overlap due or scheduled work.
- Oldest outstanding age includes leased and scheduled work. Oldest overdue age
  measures time since eligibility among currently due work, not time since creation.

The query reads at most the configured limit plus one matching rows. An extra row
sets `Truncated`. Invalid timestamp metadata also makes `Complete` false. Counts
from a truncated sample are lower bounds; oldest timestamps and ages describe only
the sample. Always export completeness with the values. Increase the bound within
its supported range or inspect the store when coverage is incomplete.

Coverage is `bounded_retained_unfinished_intents`: it says nothing about intents
never recorded, removed rows, terminal history, or other deployments. Callback
confirmation concerns Gregale acknowledgment receipt; it does not independently
prove business processing, ordering, or exactly-once side effects. No payloads,
tokens, or claim secrets appear in these summaries.

## Watching sustained conditions

Thresholds are opt-in counts: zero disables a condition, at least one must be
positive, and neither may exceed the scan limit. Metrics are `due_backlog` and
`stalled_recovery`; equality breaches the threshold.

The watch polls sequentially with a per-query timeout. Defaults are five seconds
between completed polls, a five-second query timeout, freshness of the greater of
three poll intervals or two query timeouts, and a sustain interval of the greater
of 30 seconds or one poll interval. Poll intervals support 100 ms–one minute;
query timeouts support 10 ms–30 seconds. Freshness must cover at least two poll
intervals and the query timeout, up to five minutes. Sustain must cover at least
one poll interval, up to 24 hours.

Both firing and resolution require at least two distinct, fresh, complete
observations spanning the sustain interval. Failed reads, incomplete coverage,
future/stale/repeated/regressing database timestamps, or excessive polling gaps
reset confirmation. They produce `unknown`, or `confirmation_lost` for a firing
condition, never `resolved`. Fresh evidence must establish a condition again.
Repeated unchanged unknown reasons are suppressed.

Notifications are synchronous local callbacks. Slow callbacks delay polling and
can invalidate continuity. The caller owns cancellation and application lifetime;
cancellation returns the context error without resolving alerts. Alert state is
in memory and resets on restart. There is no notification transport, durable
incident identity, checkpoint, or alert outbox. Applications needing those
properties must provide them separately.
