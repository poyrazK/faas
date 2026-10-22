# Sustained consumer rate and deadline-risk alerts

The Go SDK delivery-health watch supports opt-in `RateRisk` conditions using
[full pending summaries](event-subscription-pending-summary.md) and optional
[read-only rate previews](event-subscription-rate-preview.md). This adds no server
scheduler, automatic notification destination, configuration mutation or migration.
The watch reads health and a full pending summary (nested in a deadline-window
response when that option is selected), then a matching rate preview
only when modeled-risk monitoring is selected. Configuration changes between the
summary and preview make model evidence unknown; summary-based evidence remains
independent. The next poll refreshes both.

```go
result, err := client.WatchEventConsumerDeliveryHealth(ctx, app, subscriptionID,
    faas.EventDeliveryHealthWatchOptions{
        Window:   "5m",
        Interval: 5 * time.Second,
        Timeout:  10 * time.Minute,
        MaxAge:   30 * time.Second,
        RateRisk: &faas.EventDeliveryRateRiskOptions{
            ThrottledGrowth: 1,
            DeadlineRisk:    1,
            DeadlineWithin:  5 * time.Minute,
            Sustain:         time.Minute,
            RecoverySustain: 2 * time.Minute,
        },
        OnProgress: func(progress faas.EventDeliveryHealthWatchResult) error {
            // Inspect RateEvidence and Conditions; handle progress.Alerts.
            return nil
        },
    })
```

Set a count or DeadlineWithin to zero to disable that condition. All three disabled is invalid unless DeadlineWindow count conditions are enabled.
DeadlineWithin accepts whole milliseconds from 100 ms through 24 h.
Growth thresholds range from 1 through the exact JSON count bound 9007199254740991.
Modeled-risk thresholds range from 1 through the preview sample bound of 1000. Warning and recovery
durations default independently to one minute; explicit durations use the existing
100 ms..24 h watch bounds. Options are copied before polling begins. With RateRisk
absent, existing watch behavior and request count remain unchanged. Node and Python
watch helpers do not yet expose this Go SDK option.

## Conditions and interpretation

- `pending_summary.throttled_growth_recipients` measures positive pending-count growth
  between consecutive complete full summaries, while the shared token bucket is rate
  limited at both observations. The threshold is recipients **per observation
  interval**, not recipients per second. The first summary establishes a baseline;
  it cannot establish growth. Policy changes, read failures and sample gaps discard
  the baseline. Legacy per-second pacing is reported as a contributor; use existing
  `WaitingReasonCounts` or `WaitingReasonDurations` for
  `subscription_rate_limited` to monitor legacy throttling independently.
- `rate_preview.deadline_risk_recipients` measures the preview's aggregate modeled
  deadline-risk count, including already expired recipients. It warns about the
  model's predicted misses, not every recipient with a nearby deadline. The model
  remains advisory: scheduling, future work, retries and blocking controls can
  change actual outcomes. A count below threshold is not a deadline-safety proof.

- `pending_summary.deadline_within_millis.<window>` warns when the earliest effective deadline in the full pending set is within `DeadlineWithin` of the observation, including
  already expired rows. This includes paused deliveries that the rate-only model
  considers safe. Its value is boolean (0 or 1), with threshold 1. Choose a warning
  sustain duration that fits the warning window; sampled evidence does not prove
  continuous observation.

Both opening and resolution require at least two fresh, advancing eligible metric
observations spanning their configured duration. Growth normally needs at least
three full summaries: one baseline and two comparable growth observations. Current
firing incidents remain firing during pending recovery. Recovery means the selected
condition stayed below threshold; a resolved growth incident does not mean the
bucket is unthrottled or the backlog is empty.

## Evidence and contributors

`RateEvidence` reports summary eligible/unknown status, reason, the observed full
summary and optional preview, independent preview availability/reason,
growth availability/count/reason, deadline-evidence availability/reason, and a bounded contributor summary. Contributors
include shared bucket throttling, subscription pause, circuit state, configured
legacy pacing, saturated consumer/app/account capacity, active leases and scheduled
retries. They describe observed controls, not proof of a unique root cause. These
controls are not changed by monitoring.

Summary identity, explicit full coverage, count/deadline consistency, controls and
timestamps must validate. Complete full summaries support growth and deadline-window
alerts above 1,000 pending recipients. Missing data, read timeouts, stale/future or
nonadvancing clocks, unsupported endpoints and incomplete evidence produce
unknown/confirmation_lost rather than successful resolution. Freshness and maximum
gap checks apply independently of alert duration. There is no sampled fallback.

Modeled-risk alerts additionally require a matching complete preview and valid
risk-counter coverage. Pending-sample truncation makes only that model unknown.
Risk-detail truncation remains independent: aggregate risk counts can drive the
modeled condition when only the first 100 identities are returned. Neither missing
counts nor truncation becomes zero. Already expired rows remain facts as of the
observation; the watcher performs no recovery or deadline extension.

The chosen deadline window is part of checkpoint condition identity. Summary-based
growth/window metrics have different names from the earlier sampled conditions, so
those checkpoints cannot resume as full-summary sessions. Settle their pending
alerts with the earlier compatible client before starting a new session.
Each new alert includes immutable `rate_constraints` context. Progress includes the
full summary and optional bounded preview; the checkpoint alert does not copy event identities or
payloads. On unavailable evidence, the context is `evidence_unavailable`. Unknown
constraint labels fail closed instead of being copied into alert summaries.

## Durable delivery and resume

The conditions reuse the existing bounded alert outbox, incident IDs, monotonic
sequences, acknowledgements and progress-before-notification workflow. Set
`DurableAlerts: true`, persist `progress.Checkpoint` atomically in OnProgress before
delivering its pending notifications, and acknowledge exact incident/sequence pairs
only after downstream handling succeeds. The watch itself sends no messages.

Rate-risk checkpoints use the existing version 2 sustained-recovery representation.
Validation includes the selected counts, warning/recovery durations and bounded
contributor labels. Resume requires identical options and condition selection.
Older clients reject the unknown metric names rather than treating them as clear.

A resumed incident starts unknown. Historical observation timestamps guard against
replayed samples, but pending counts, comparison baselines and sustain timers are
rebuilt from fresh evidence. A valid first growth baseline waits for a comparison;
it does not resolve a resumed incident. Subsequent evidence loss closes confirmation
with `confirmation_lost`; it never claims recovery. Resume cannot restore live proof
from a checkpoint, nor can callbacks mutate the watch's internal progress snapshots.

For complete upcoming counts and separate expired waiting-reason partitions, use
[deadline-window monitoring](event-subscription-deadline-window.md). It adds optional
sustained count alerts and matching relative-window/reason triage filters.

Deadline-window count alerts can also select independent upcoming/expired
waiting-reason thresholds. See [reason-specific configuration and recovery
semantics](event-subscription-deadline-window.md#alerts-for-selected-waiting-reasons).

The Go watch also exposes [confirmation progress](event-delivery-confirmation-progress.md)
for these conditions, including pending sustain, growth baselines, resume evidence
and notification-outbox blockage.
