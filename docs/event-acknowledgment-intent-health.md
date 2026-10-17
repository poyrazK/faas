# Acknowledgment intent recovery health

The Go, Node and Python SDKs inspect the application-owned acknowledgment intent
store. Go and Node also watch for sustained recovery backlog. Install the schema
and configure recovery with [Go intents](event-acknowledgment-intents.md),
[Node intents](event-acknowledgment-node-intents.md) or
[Python intents](event-acknowledgment-python-intents.md) first.
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

## Node summary

```js
import { getEventAcknowledgmentIntentHealth } from '@gregale/sdk-node';

const health = await getEventAcknowledgmentIntentHealth(pool, accountId, appId, {
  scanLimit: 1000,
  stallAfterMs: 300000,
  signal,
});
// Always export coverage and completeness alongside counts and ages.
recordRecoveryHealth(health);
```

Use trusted owner UUIDs and the existing `EventConsumerPool` interface. No platform
client or credentials are needed for this local database read. The optional
`scanLimit` accepts integers from 1 through 10000, default 1000; `stallAfterMs`
accepts whole milliseconds from 1000 through 86400000, default 300000. Omit an
option to select its default; zero is rejected. The output includes the chosen
`scanLimit` and `stallAfterSeconds`.

Node returns the same metrics and coverage semantics as Go using camelCase:
`outstanding`, `pending`, `unconfirmed`, `due`, `leased`, `scheduled`,
`expiredClaims`, `stalled`, `invalidRows`, `oldestOutstandingAt`,
`oldestOutstandingAgeSeconds`, `oldestEligibleAt` and `oldestOverdueAgeSeconds`.
The immutable result also binds normalized `accountId` / `appId` and database
`observedAt`, with `coverage`, `complete` and `truncated`. Timestamps are ISO
strings; absent oldest timestamps are omitted. An empty, complete sample has zero
counts and ages. Terminal history does not affect these counts.

This helper executes one metadata SELECT using one snapshot and database clock,
without BEGIN/COMMIT, advisory locks, claims, callbacks or business code. It
selects no tokens, claim secrets, result bodies or original events; invocation
metadata is used only for deterministic sampling and is not returned. Storage
errors and malformed aggregate evidence throw the existing sanitized
`EventAcknowledgmentIntentError` with code `event_acknowledgment_intent_store`.
SQL diagnostics are not retained.

Cancellation is cooperative before/after the query. A signal that aborts during
pool acquisition or an active driver query does not make that driver operation
return immediately; configure bounded acquisition, query and PostgreSQL statement
timeouts. The connection is released only after the operation finishes; a failed
query discards it. The health read neither installs schema nor schedules polling.
It can inspect int64 replay generations beyond the Node callback worker's numeric
range because it does not decode or send a generation fence.

Export `complete` and `truncated` with every metric. An incomplete sample cannot
establish that all recovery work is healthy, even when `stalled` is zero. A stopped
recovery host can leave ingestion and business processing working while due and
stalled callback backlog grows. This summary describes that local backlog; it does
not confirm platform delivery or prove the recovery host's availability. For
sustained alerts, use the [Node watch](#node-watch). See the
[read-only example](../examples/event-acknowledgment-node/README.md#observe-recovery-health).

## Python summary

```python
from dataclasses import asdict
from faas_sdk import (
    EventAcknowledgmentIntentHealthOptions,
    aget_event_acknowledgment_intent_health,
)

health = await aget_event_acknowledgment_intent_health(
    connection, account_id, app_id,
    EventAcknowledgmentIntentHealthOptions(scan_limit=1000, stall_after_ms=300000),
    stop_event=shutdown,
)
record_recovery_health(asdict(health))
# Export complete/truncated/coverage with every count and age.
```

Use trusted owner UUIDs and an exclusively leased, idle autocommit psycopg async
connection. No platform client or platform credentials are needed. Keep monitoring
connections separate from active business or recovery operations. The options
dataclass is immutable; values are copied before the first await. `None` chooses
defaults. `scan_limit` accepts native integers 1..10000, default 1000;
`stall_after_ms` accepts whole milliseconds 1000..86400000, default 300000. Zero,
booleans, floats and invalid option objects are rejected before database access.

The frozen `EventAcknowledgmentIntentHealth` uses snake_case fields matching the
same Go/Node classification: `outstanding`, `pending`, `unconfirmed`, `due`,
`leased`, `scheduled`, `expired_claims`, `stalled`, `invalid_rows`,
`oldest_outstanding_at`, `oldest_outstanding_age_seconds`, `oldest_eligible_at`,
and `oldest_overdue_age_seconds`. It includes normalized `account_id`/`app_id`,
database `observed_at`, `scan_limit`, `stall_after_seconds`, `coverage`, `complete`
and `truncated`. Timestamps are UTC RFC 3339 strings with microseconds; absent
oldest timestamps are `None`. Counts are native integers and ages are finite,
nonnegative seconds. An empty complete sample has zero counts/ages.

The helper runs one metadata SELECT with the same snapshot/clock, deterministic
sampling and limit-plus-one truncation evidence as Node. It selects no platform
tokens, claim secrets, results or original events. Invocation/generation/attempt
columns order the sample and are not returned. No explicit transaction commands,
advisory locks, claim updates, callbacks or business work are performed.

Malformed counts, inconsistent aggregates, nonfinite ages or unrepresentable
timestamps raise the sanitized `EventAcknowledgmentIntentError` with code
`event_acknowledgment_intent_store`. Invalid timestamp metadata detected by the
query makes `complete=False`. Finite aggregate timestamps that the Python driver
cannot represent also fail the read; the helper never fabricates healthy evidence.
Driver/cursor failures close the exclusively leased connection; use the pool's
discard path. Driver diagnostics and arbitrary causes are not retained.

`stop_event` and native task cancellation use the worker's cooperative cancellation
category, `EventAcknowledgmentWorkerCancelled`. Active SQL/cursor cleanup is
shielded and joined before cancellation returns. Cancellation checks before/after
driver awaits do not impose a hard SQL timeout. Configure bounded acquisition,
query, PostgreSQL statement and close timeouts; drain the operation before
closing its connection/event loop. A failed driver cancellation also discards
the connection. The helper installs no schema or polling loop.

Always retain coverage/completeness alongside the values. A truncated or invalid
sample cannot establish health, even if `stalled` is zero. This summary concerns
retained local callback recovery, not business success or platform completion.
Python polling and alert policy remain application-owned. See the
[Python observation example](../examples/event-acknowledgment-python/README.md#observe-recovery-health).

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

Go notifications are synchronous local callbacks; Node callbacks are awaited
sequentially. Slow callbacks delay polling and can invalidate continuity. The
caller owns cancellation and application lifetime; cancellation returns the
context error or abort reason without resolving alerts. Alert state is
in memory and resets on restart. There is no notification transport, durable
incident identity, checkpoint, or alert outbox. Applications needing those
properties must provide them separately.

## Node watch

```js
import {
  watchEventAcknowledgmentIntentHealth,
  EventAcknowledgmentIntentWatchCallbackError,
} from '@gregale/sdk-node';

const controller = new AbortController();
// The host calls controller.abort() on shutdown.
try {
  await watchEventAcknowledgmentIntentHealth(pool, accountId, appId, {
    signal: controller.signal,
    health: { scanLimit: 1000, stallAfterMs: 300000 },
    dueThreshold: 100,
    stalledThreshold: 1,
    sustainMs: 30000,
  }, async (alert, signal) => {
    signal.throwIfAborted();
    await recordRecoveryAlertLocally(alert, signal);
  });
} catch (error) {
  if (controller.signal.aborted) throw controller.signal.reason;
  if (error instanceof EventAcknowledgmentIntentWatchCallbackError) {
    // error.alert is metadata only; notification outcome is unconfirmed.
    // The application decides how to reconcile or retry its own notification.
  }
  throw error;
}
```

The watch starts with one immediate read and runs until cancellation or a local
callback failure. It performs only the read-only health query; it never claims or
repairs intents, sends platform acknowledgment callbacks, or executes business work.
Use a recoverable monitoring host; the function does not keep a parked VM awake.
One watch has at most two conditions, with options copied before its first await.

| Option | Range and default |
| --- | --- |
| `health.scanLimit`, `health.stallAfterMs` | Same bounds/defaults as the Node summary. |
| `dueThreshold`, `stalledThreshold` | Integer counts 0..scanLimit; default 0 disables. Enable at least one. |
| `pollIntervalMs` | Whole milliseconds 100..60000; default 5000 after completed reads and callbacks. |
| `queryTimeoutMs` | Cooperative read deadline, whole milliseconds 10..30000; default 5000, including acquisition. |
| `maxAgeMs` | Whole milliseconds from max(2*pollIntervalMs, queryTimeoutMs) through 300000; default max(3*pollIntervalMs, 2*queryTimeoutMs). |
| `sustainMs` | Whole milliseconds pollIntervalMs..86400000; default max(30000, pollIntervalMs), for firing and resolution. |
| `signal` | Optional host cancellation. Without it the host must otherwise own process lifetime. |

Omit an option to choose its default. Zero is accepted only for disabled thresholds.
Threshold equality breaches. Complete observations must have distinct, advancing
database timestamps, be no later than the local wall clock, and be within
`maxAgeMs`. Poll continuity and sustain duration use the local monotonic clock.
A gap longer than `maxAgeMs` resets proof even when the latest database observation
is fresh. Both firing and resolution need at least two eligible samples spanning
`sustainMs`. Crossing back over the threshold resets the relevant proof.

Each alert is immutable metadata with `accountId`, `appId`, `metric`, `change`,
`reason`, `at`, `value`, `threshold`, and optional `health`. Metrics are
`due_backlog` and `stalled_recovery`. `at` records local observation completion;
`health.observedAt` remains the database observation time. No token, payload,
invocation identity, SQL diagnostic or raw read error is included.

| Change | Meaning |
| --- | --- |
| `firing` / `threshold_exceeded` | Sustained complete evidence confirmed a breach. |
| `resolved` / `threshold_cleared` | A firing condition cleared for the full confirmation interval. |
| `unknown` | Evidence is unavailable: `read_failed`, `incomplete`, `stale`, `out_of_order`, or `sample_gap`. |
| `confirmation_lost` | A firing condition lost evidence; the reason identifies the loss. This does not resolve it. |

Repeated unchanged unknown reasons are suppressed. After evidence loss the
condition starts fresh confirmation; a renewed breach must sustain again. Initial
healthy reads, and healthy reads after an unknown state, do not emit a resolution
for an earlier incident. Applications must retain their own incident/reconciliation
policy; this watch persists no incident identity. A read failure has no `health`
and `value` is zero, which is not evidence of an empty backlog. Incomplete/stale
snapshots may be attached to unknown alerts but are not confirmation evidence.

The per-read deadline aborts the health call cooperatively. An operation that
settles after its deadline becomes `read_failed` even if its timer callback was
delayed. The watch awaits actual acquisition/query completion before releasing
the connection or starting another poll. It never races an active query and
abandons its connection. Configure driver acquisition/query timeouts and
PostgreSQL statement timeouts to bound an unresponsive operation. Cancellation
likewise waits for active driver work, then rejects with the host signal reason
without emitting a final resolution.

Local `notify(alert, signal)` callbacks may be synchronous or async; the watch
awaits them in order and does not retry them. A callback failure stops the watch
with `EventAcknowledgmentIntentWatchCallbackError`, whose `alert` is the failed
metadata notification. The raw cause is omitted. If cancellation occurs while a
callback is running, the watch waits for it and returns the abort reason. Callback
work must observe its signal and return promptly. Slow callbacks delay later
notifications and can cause a sample gap on the next read. Notifications generated
from one observation retain that observation-completion time if delivery is slow.

State resets on restart. There is no durable alert outbox, notification transport
or automatic reattachment of an old incident. The host owns scheduling, persistence
and downstream deduplication when needed. See the
[monitoring integration example](../examples/event-acknowledgment-node/README.md#watch-sustained-recovery-conditions).
Local validation is build, generation and static checks; database timing,
cancellation and notification behavior require hosted acceptance before release.

For stable incident identity and replayable pending notifications across restarts,
use the separate opt-in [checkpointed Node watch](event-acknowledgment-node-checkpoints.md).
The ordinary watch above retains its in-memory behavior.
