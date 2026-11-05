# Count deadline urgency and observed waiting reasons

`GET /v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-control/deadline-window?within_millis=300000`
reads a full pending summary plus upcoming and expired deadline cohorts in one
read-only Repeatable Read snapshot. It requires the existing read scope and MFA
policy and returns `Cache-Control: no-store`. Go clients expose
`GetEventSubscriptionDeadlineWindow`; generated Node and Python clients expose the
same operation. No event payloads or recipient identities are returned.

`within_millis` is mandatory, accepts whole milliseconds from 100 through
86,400,000 (24 hours), and defines `window_end_at = summary.observed_at + horizon`.
Unknown, repeated and malformed parameters are rejected.

- `upcoming_recipients` counts effective deadlines strictly after the observation
  and strictly before the window end. Equality with the upper boundary is excluded.
- `summary.already_expired_count` separately counts deadlines at or before the
  observation, regardless of how long ago they expired. These never count as upcoming.
- `earliest_upcoming_deadline_at` is present exactly when the upcoming count is nonzero.
- `upcoming_waiting_reasons` and `expired_waiting_reasons` are exact, disjoint
  partitions of their respective counts. Every known backlog waiting-reason key
  is present, including explicit zero keys.

The nested `summary` includes all retained captured pending application recipients,
including those without deadlines or beyond the selected horizon, along with the
same-snapshot controls, earliest effective deadline and full counts. Cohorts include
only recipients with effective delivery-age deadlines. Existing captured policy,
ordered-work exclusions and replay age overrides apply. Processing recipients,
future arrivals, uncaptured publications and purged history are outside coverage.

Waiting reasons reuse backlog precedence for pause/circuit/throttle controls,
capacity, active receipt leases and retry backoff. They are observations, not proof
of unique causes. A `ready` row can already be expired; readiness labels do not
certify successful admission or execution. The summary's general contributor flags
cover its whole pending scope, while each reason partition covers its own cohort.

## Completeness and resource budgets

There is no 1,000-row sampling cap. PostgreSQL groups the scoped pending set by
observed reason and returns bounded reason groups; the in-memory store retains
only counters and minimum timestamps. The request budget is five seconds, including
direct store calls, with a transaction-local four-second statement timeout. Query
work still grows with the scoped backlog and can exceed its budget. A timeout
returns 504 without partial counts. No migration or materialized counter ledger
is introduced.

Inspect `summary.complete` before using the response for confirmation. Invalid
clocks make coverage incomplete. Missing reason keys, unknown labels, inconsistent
partitions, stale/nonadvancing timestamps or a mismatched horizon cannot establish
alert health or recovery. The Go count validator
`EventSubscriptionDeadlineWindowCountsComplete` checks counter/partition consistency;
watch confirmation also checks scope, controls and freshness. Zero is an explicit
complete observation, not a substitute for a failed read.

## Sustained Go watch alerts

```go
options := preview.EventDeliveryHealthWatchOptions{
    Window: "5m",
    Interval: 5 * time.Second,
    Timeout: 10 * time.Minute,
    MaxAge: 30 * time.Second,
    RateRisk: &preview.EventDeliveryRateRiskOptions{
        DeadlineWindow: &preview.EventDeliveryDeadlineWindowOptions{
            Within: 5 * time.Minute,
            Upcoming: 50,
            Expired: 1,
        },
        Sustain: time.Minute,
        RecoverySustain: 2 * time.Minute,
    },
}
```

Set either total count to zero to disable that condition. At least one total or
reason-specific count must be enabled (see the reason options below).
Counts accept 1..9007199254740991. A positive whole-millisecond horizon is required
for both configurations. Existing `Sustain` and `RecoverySustain` defaults/bounds
apply independently. Both warning and recovery require at least two fresh,
advancing eligible observations spanning their respective duration. Choose durations
that fit the desired warning lead time.

The metric names are `deadline_window.upcoming_recipients.<millis>` and
`deadline_window.expired_recipients.<millis>`. Threshold and horizon are checkpoint
configuration, so resume requires the same selection and values. The conditions
reuse version 2 sustained-recovery checkpoints, durable alert delivery, incident
IDs, bounded queues and immutable contributor summaries. Older clients reject
unknown metrics. Existing summary/model metric identities remain unchanged.

With `DeadlineWindow` selected, the watch reads this endpoint in place of the
separate pending-summary request. Its nested summary also drives any selected
summary growth or earliest-deadline signals. A modeled-risk preview remains an
additional independent request when selected. Without this option, existing watch
request behavior is unchanged. An unsupported or failed window endpoint stays
unknown; there is no fallback to sampled counts. Invalid cohort partitions make
window conditions unknown while an otherwise valid full summary can still support
the other summary conditions.

Progress exposes `RateEvidence.DeadlineWindow` and independent window evidence
availability/reason. Options are copied before polling; progress snapshots deep-copy maps before callbacks.
Alerts retain the count value and bounded summary contributor context, not mutable
reason maps or payloads. Persist progress before downstream notification when
using durable alerts; the watch sends no messages itself.

These are rolling aggregate cohorts, not fixed delivery membership. An upcoming
count can fall because work completed, acquired an age override, was pruned, or
became expired. Resolution only confirms the selected count stayed at or below its recovery threshold;
it does not certify delivery success. Enable the separate expired-count condition
when aging out must remain visible. Expired-count resolution also does not prove
business completion. Neither condition predicts or extends deadlines.

## Investigate a cohort

The [deadline triage listing](event-subscription-deadline-triage.md) accepts
`within_millis=300000&waiting_reason=subscription_paused`. A relative window selects
upcoming rows strictly after the first page `window_at` and before its window end.
Omitted/`all` status normalizes to `upcoming`; `expired` with a relative window is
rejected. Use `status=expired&waiting_reason=...` without `within_millis` to inspect
expired rows. The reason filter applies before pagination and observes current
waiting reasons on each page.

To inspect the earlier count's absolute upcoming interval, use
`deadline_from = summary.observed_at + 1 microsecond` and
`deadline_before = window_end_at`, with no relative horizon and status `all`.
The microsecond step excludes equality at the expired boundary. These filters
align time definitions but do not freeze membership or guarantee the same count:
triage runs later, and routing state, reasons and retention can change. Preserve
all filters and page size across cursor pages. Existing unfiltered cursors keep
their original scope; adding a filter requires a new traversal.

## Alerts for selected waiting reasons

Go watches can select independent thresholds for observed reasons inside either
cohort, without enabling total-count alerts:

```go
DeadlineWindow: &preview.EventDeliveryDeadlineWindowOptions{
    Within: 5 * time.Minute,
    UpcomingReasons: map[string]int64{
        "subscription_paused": 10,
        "capacity_consumer": 25,
    },
    ExpiredReasons: map[string]int64{
        "subscription_paused": 1,
    },
},
```

`UpcomingReasons` and `ExpiredReasons` accept the known backlog waiting-reason
keys. Values 1..9007199254740991 enable a condition; an omitted key or zero disables
it. Negative/oversized values and unknown reason names are rejected, including
unknown names with a zero value. At least one total or reason-specific count must
be enabled. The horizon and existing warning/recovery duration bounds still apply.
Option maps are cloned before validation and polling. Callers must not mutate
options concurrently with the initial call.

Each selected cohort/reason has its own incident, warning proof and recovery proof.
The shared `Sustain` and `RecoverySustain` durations configure those independent
timers. Positive reason conditions are ordered by cohort and then reason name,
so map insertion order cannot change checkpoint resume. Total conditions retain
their existing identities and order. Omitted and zero-valued reason entries do not
create conditions and need not be preserved as checkpoint configuration.

For example, the paused/upcoming condition above has metric identity
`deadline_window.upcoming_recipients.300000.reason.subscription_paused`. The
checkpoint validates the cohort, canonical horizon, known reason, count threshold
and sustained-recovery policy. Resume requires the same enabled selection and
values; older clients reject the new identities. A reason suffix is not permitted
to be empty or contain an unknown label.

All selected conditions reuse the existing deadline-window response. There is no
additional request per reason and no new endpoint or migration. Confirmation still
requires full, correctly scoped, fresh, advancing evidence and exact partitions
containing every known reason, including explicit zero keys. Missing/unknown keys
or incomplete evidence cause unknown/confirmation_lost, never a fabricated zero
or successful recovery. Progress snapshots retain the existing deep-copy behavior.
The durable alert outbox and reserved closing slots apply independently to each
incident. With the current 14 known labels, at most 28 reason conditions plus two
optional total conditions can be selected in a deadline window; the existing
notification queue bound is unchanged.

A reason-specific alert resolves when its own count stays at or below its recovery threshold. Work
can change waiting reason while remaining pending, move from upcoming to expired,
or leave retained coverage without completing business processing. A resolution
therefore does not mean deliveries succeeded or the overall backlog recovered.
Monitor both cohorts and relevant reasons, or add total thresholds, when those
transitions must stay visible. Total and reason alerts can fire together for the
same underlying work and remain separate incidents. The observed reason identifies
its partition, not a unique root cause. Use the triage `waiting_reason` filter to
investigate current identities.

The Go watch also exposes [confirmation progress](event-delivery-confirmation-progress.md)
for these conditions, including pending sustain, growth baselines, resume evidence
and notification-outbox blockage.

## Separate opening and recovery counts

Each deadline count can use a lower recovery threshold to avoid opening and closing
incidents repeatedly near the warning boundary. For example:

```go
upcomingRecovery := int64(20)
options := preview.EventDeliveryHealthWatchOptions{
    Window: "5m",
    Interval: 5 * time.Second,
    Timeout: 10 * time.Minute,
    MaxAge: 30 * time.Second,
    RateRisk: &preview.EventDeliveryRateRiskOptions{
        DeadlineWindow: &preview.EventDeliveryDeadlineWindowOptions{
            Within: 5 * time.Minute,
            Upcoming: 50,
            UpcomingRecovery: &upcomingRecovery,
            UpcomingReasons: map[string]int64{"subscription_paused": 10},
            UpcomingReasonsRecovery: map[string]int64{"subscription_paused": 2},
        },
        Sustain: time.Minute,
        RecoverySustain: 2 * time.Minute,
    },
}
```

The total opens after at least two eligible observations at or above 50 spanning
one minute. Once firing, it stays open at 21..49 and resolves only after at least
two observations at or below 20 spanning two minutes. A value above 20 resets the
recovery timer. The paused-reason incident independently opens at 10 and recovers
at 2, using the same duration settings. Before an incident opens, values below its
warning threshold remain clear. Resumed incidents require fresh warning or recovery
confirmation; observations in the middle band keep them unknown.

`UpcomingRecovery` and `ExpiredRecovery` are optional pointers. Nil defaults to
warning minus one; a pointer to zero explicitly requires the count to reach zero.
`UpcomingReasonsRecovery` and `ExpiredReasonsRecovery` are optional maps keyed by
known waiting reasons. An omitted key defaults to that reason's warning minus one;
an explicit zero requires zero. Every supplied recovery count must be nonnegative
and strictly below its enabled warning count. Unknown reasons, disabled conditions,
and equal, greater or negative recovery settings are rejected. Recovery counts for
totals and reasons do not inherit from each other. Pointers and maps are copied
before validation and polling; do not mutate inputs concurrently with the initial call.

The effective recovery threshold already appears in conditions, alerts and version 2
checkpoints. Resume requires the same effective warning and recovery policy.
Omitting a recovery setting and explicitly supplying its default are equivalent.
Existing checkpoints using warning minus one continue to work. Older clients reject
checkpoints with a custom lower boundary; upgrade the resuming client first. Warning
and recovery timers are never restored as fresh proof. Confirmation diagnostics keep
showing pending recovery only while its boundary is satisfied. This option adds no
requests, endpoint, schema changes or delivery actions, and does not change other
rate-risk metrics. Aggregate count recovery still does not certify delivery success.

## CLI watch

The delivery-health command exposes the same total and reason conditions:

```sh
gregale events subscription-delivery-health APP SUB --watch \
  --deadline-window 5m \
  --deadline-upcoming-threshold 50 --deadline-upcoming-recovery 20 \
  --deadline-expired-threshold 1 --deadline-expired-recovery 0 \
  --deadline-upcoming-reason subscription_paused=10 \
  --deadline-upcoming-reason-recovery subscription_paused=2 \
  --deadline-sustain 1m --deadline-recovery-sustain 2m \
  --interval 5s --timeout 10m --confirmation-progress \
  --checkpoint ./delivery-health.json --alerts-json
```

Use `--deadline-expired-reason REASON=N` and
`--deadline-expired-reason-recovery REASON=N` for expired reason conditions. Reason
flags are repeatable for different known keys; duplicates within one flag are
rejected. Warning zero disables a condition; recovery zero explicitly requires
zero. Omitted recovery settings default to warning minus one. Recovery entries
require an enabled warning for the same cohort and reason; total settings do not
supply reason defaults. The expired count covers all expired deadlines, independently
of the horizon.

Every deadline flag requires `--watch`, a valid `--deadline-window` and at least
one positive total, reason or ratio warning. The horizon accepts whole milliseconds
100ms..24h. Warning and recovery durations independently default to one minute and
accept 100ms..24h; explicit zero durations are invalid. Counts use the same exact
integer bound as the Go options. Existing watch freshness and timeout limits apply.
Invalid deadline combinations are rejected before authentication, polling or
checkpoint preparation. Other delivery-health thresholds may be enabled alongside
the deadline conditions.

Final text output includes each deadline condition's state, last observed count,
warning and inclusive recovery boundaries, and confirmation diagnostics. With
`--confirmation-progress`, these details also print after every observation. The
last count is not fresh proof when state is unknown; inspect confirmation reasons.
Lifecycle alerts remain on stderr, including with `--alerts-json`. Omit live text
progress with global `--json`; the final JSON result includes conditions, evidence
and confirmations. This command does not send notifications itself.

Existing `--checkpoint`, `--resume`, `--ack-inbox` and offline alert queue commands
apply without a second checkpoint format. Checkpoints are persisted before alerts
are emitted. Resume with the same effective horizon, enabled counts, recovery
boundaries and durations; repeatable flag order does not matter. Resume never
credits downtime or saved confirmation timers. A failed read stays unknown and
cannot clear an incident. The CLI does not fall back to sampled counts or take
recovery actions.

## Node watch

Node applications can use `watchEventSubscriptionDeadlines` for deadline-only
sustained count conditions, recovery boundaries, confirmation progress and durable
alert acknowledgments. It uses a separate Node checkpoint format and the existing
full-count endpoint. See [Node watch configuration and persistence](event-deadline-node-watch.md).

## Python watch

Python exports `awatch_event_subscription_deadlines` with independent total/reason
warning and recovery counts, fresh confirmation progress and durable alert
acknowledgments. Its checkpoint format is Python-specific. See
[async configuration, cancellation and persistence](event-deadline-python-watch.md).

## Expired ratio alerts in Go

`DeadlineWindow.ExpiredRatio` selects expired / all pending deadline-bearing
recipients with explicit minimum cohort size, integer basis-point warning/recovery
boundaries and sustained fresh confirmation. Low/empty cohorts remain unknown.
See [ratio configuration, exact boundaries and checkpoint compatibility](event-deadline-ratio-alerts.md).

The CLI also accepts percentage-based ratio settings:
`--deadline-expired-ratio-threshold`, `--deadline-expired-ratio-recovery`, and
`--deadline-expired-ratio-min-recipients`. See [ratio CLI usage](event-deadline-ratio-alerts.md#cli-ratio-watch).
