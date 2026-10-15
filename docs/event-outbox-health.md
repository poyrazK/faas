# Outbox recovery health and local alerts

The Go SDK can inspect retained unpublished outbox rows and watch sustained
conditions. Configure the [application-owned outbox](event-outbox.md) first.
These helpers only read local metadata. They never publish, claim, reconcile,
resume, install, or prune work.

```go
health, err := faas.GetEventOutboxHealth(ctx, db, accountID, appID,
    faas.EventOutboxHealthOptions{
        ScanLimit: 1000,
        StallAfter: 5 * time.Minute,
    })
if err != nil { return err }
// Export health.Complete and health.ObservedAt alongside counts and ages.
_ = health

return faas.WatchEventOutboxHealth(ctx, db, accountID, appID,
    faas.EventOutboxWatchOptions{
        DueThreshold: 100,
        StalledThreshold: 1,
        BlockedThreshold: 1,
        ExpiredThreshold: 1,
        Sustain: 30 * time.Second,
    }, func(alert faas.EventOutboxAlert) {
        // Record locally or hand off promptly to the application's alert system.
    })
```

## Coverage and classification

One owner-scoped SQL statement uses one PostgreSQL snapshot and database clock.
It samples unpublished `pending`, `unconfirmed`, and `blocked` rows, ordered by
creation time then exact producer key under `C` collation. Published history
cannot crowd out unresolved work. Defaults are 1,000 rows and a five-minute stall
interval; supported ranges are 1–10,000 rows and one second–24 hours.

- `Outstanding` is the sampled unpublished count. `Pending`, `Unconfirmed`, and
  `Blocked` divide it by stored state.
- `Expired` counts unpublished rows whose original retry deadline has passed,
  including rows not yet visited by recovery. It can overlap `Blocked` or the
  other stored-state counts; it is not a new stored state.
- `Due`, `Leased`, and `Scheduled` cover active pending/unconfirmed work whose
  retry deadline is still in the future. Live claims are leased. Work without a
  live claim is due once eligible while publishing is enabled, otherwise future
  work is scheduled. Eligible work held by a scope pause is counted in PausedDue.
- Eligibility is the latest of creation, next attempt, and claim expiry. `Stalled`
  counts active work overdue for at least `StallAfter`; blocked, expired,
  future-scheduled, live-claimed, and scope-paused work cannot be stalled.
- `ExpiredClaims` counts elapsed claims and can overlap expired or due work.
- Oldest outstanding age includes blocked, expired, scheduled and leased work.
  Oldest eligible time and overdue age include eligible active work waiting
  without a live claim, including PausedDue while publishing is paused.

The query reads at most `ScanLimit+1` matching rows, aggregates the first
`ScanLimit`, and sets `Truncated` when an extra row exists. Invalid timestamp
metadata also makes `Complete` false. Truncated counts are lower bounds and ages
describe only the sample. Export completeness rather than presenting an
incomplete sample as a healthy total. Raise the bound within its supported range
or inspect individual rows when coverage is incomplete.

Coverage is `bounded_retained_unpublished_outbox`. It excludes published/pruned
rows, never-staged events, other owner scopes, and other application databases.
`ObservedAt` is the database observation instant; callers must assess freshness
relative to their clock and tolerance. Metadata and payload consistency are
separate: the summary does not read or verify event content. No payload, producer
key, credential, request digest, or claim capability appears in health or alerts.
Errors are sanitized through the local outbox storage error contract.

## Sustained local notifications

Thresholds are opt-in counts. Zero disables a condition; at least one must be
positive, and none may exceed the scan limit. Equality breaches a threshold:

| Option | Metric | Value |
| --- | --- | --- |
| `DueThreshold` | `due_backlog` | Currently due active publications |
| `StalledThreshold` | `stalled_recovery` | Active publications overdue beyond the stall interval |
| `BlockedThreshold` | `blocked_publications` | Stored blocked rows |
| `ExpiredThreshold` | `expired_publications` | Unpublished rows past the original retry deadline |

Both firing and resolution require at least two distinct, fresh, complete
observations spanning `Sustain`. Failed reads, incomplete coverage, stale or
future database observations, repeated/regressing timestamps, and excessive
polling gaps reset confirmation. They emit `unknown`, or `confirmation_lost` for
a firing condition, never `resolved`. Fresh evidence must establish the condition
again; repeated identical unknown reasons are suppressed. A truncated sample does
not fire or resolve alerts, even if its lower bound exceeds a threshold.

Defaults are five seconds between completed polls, a five-second query timeout,
freshness (`MaxAge`) of the greater of three poll intervals or two query timeouts,
and sustain of the greater of 30 seconds or one poll interval. Poll intervals
support 100 ms–one minute; query timeouts support 10 ms–30 seconds. MaxAge must
cover at least two poll intervals and the query timeout, up to five minutes.
Sustain must cover at least one poll interval, up to 24 hours.

Notifications are synchronous in-process callbacks. Return promptly: slow callbacks
delay polling and can invalidate continuity. The watch polls sequentially and
bounds each query by its own timeout and the parent context. Cancellation returns
the context error without resolving incidents. The application owns process
lifetime and any transport, durable incident identity, or notification outbox.
Watch state is in memory and resets on restart.

Resolution means that the selected count stayed below its threshold. For example,
stalled work can become blocked or expired and leave the stalled count; enable the
blocked/expired conditions to observe that transition. Resolution does not prove
acceptance, successful recovery, retention, or consumer completion. Independent
recipient retry, ordering, replay and deduplication behavior does not change.

## Existing installations

Explicitly re-run `faas.EventOutboxSchema` to install the partial
`gregale_event_outbox_health` index for unpublished rows. No columns, constraints,
platform migrations, or clone registry entries change. Helpers do not perform DDL.
Application permissions and retention remain operator-owned; deleting unresolved
rows can remove them from subsequent summaries without proving publication.

## Publishing pause

Health includes PublishingPaused, PublishingRevision and optional
PublishingUpdatedAt from the same database snapshot as the sampled rows. A
missing scope row means enabled revision zero; missing storage is an error.
PublishingUpdatedAt in the future invalidates completeness. PausedDue counts
unexpired, eligible pending/unconfirmed rows without a live claim held by the
pause. Due and Stalled are zero while paused; Leased, Scheduled, Blocked, Expired,
and outstanding ages continue to describe retained work.

For fresh complete paused observations, due_backlog and stalled_recovery watches
emit unknown/confirmation_lost with reason publishing_paused rather than resolving
or confirming a stall. Invalid evidence retains its ordinary failure reason.
Blocked and expired watches continue. Resuming requires fresh sustained proof
before a due/stall condition can fire again. Pause time still contributes to
eligibility age; no schedule or deadline is moved.

Install [publishing-control storage](event-outbox-publishing-control.md) before
upgrading health readers or claim workers. Older workers do not enforce pauses.

## Publishing rate limits

[Shared rate limits](event-outbox-rate-limit.md) add RateLimitEnabled,
RateLimitRevision, PublishingRateLimited, optional RateLimitNextAt and ThrottledDue
to the same snapshot. ThrottledDue counts otherwise eligible sampled work while
not paused; Due and Stalled exclude it. PausedDue has priority when both controls
apply. Due/stall watches use unknown/confirmation_lost with reason
publishing_rate_limited, subject to existing proof requirements. Blocked/expired
conditions continue. Install rate storage before upgrading readers and workers.
