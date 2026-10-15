# Sustained consumer rate and deadline-risk alerts

The Go SDK delivery-health watch supports opt-in `RateRisk` conditions using
[read-only rate previews](event-subscription-rate-preview.md). This adds no server
scheduler, automatic notification destination, configuration mutation or migration.
The watch reads health, current delivery-control settings, then a preview of those
same settings. A configuration change between the reads makes that evidence
unknown; the next poll refreshes it.

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

Set a count or DeadlineWithin to zero to disable that condition. All three disabled is invalid.
DeadlineWithin accepts whole milliseconds from 100 ms through 24 h.
Counts range from 1 through the preview sample bound of 1000. Warning and recovery
durations default independently to one minute; explicit durations use the existing
100 ms..24 h watch bounds. Options are copied before polling begins. With RateRisk
absent, existing watch behavior and request count remain unchanged. Node and Python
watch helpers do not yet expose this Go SDK option.

## Conditions and interpretation

- `rate_preview.throttled_growth_recipients` measures positive pending-count growth
  between consecutive complete previews, while the shared token bucket is rate
  limited at both observations. The threshold is recipients **per observation
  interval**, not recipients per second. The first preview establishes a baseline;
  it cannot establish growth. Policy changes, read failures and sample gaps discard
  the baseline. Legacy per-second pacing is reported as a contributor; use existing
  `WaitingReasonCounts` or `WaitingReasonDurations` for
  `subscription_rate_limited` to monitor legacy throttling independently.
- `rate_preview.deadline_risk_recipients` measures the preview's aggregate modeled
  deadline-risk count, including already expired recipients. It warns about the
  model's predicted misses, not every recipient with a nearby deadline. The model
  remains advisory: scheduling, future work, retries and blocking controls can
  change actual outcomes. A count below threshold is not a deadline-safety proof.

- `rate_preview.deadline_within_millis.<window>` warns when the earliest sampled
  effective deadline is within `DeadlineWithin` of the observation, including
  already expired rows. This includes paused deliveries that the rate-only model
  considers safe. Its value is boolean (0 or 1), with threshold 1. Choose a warning
  sustain duration that fits the warning window; sampled evidence does not prove
  continuous observation.

Both opening and resolution require at least two fresh, advancing eligible metric
observations spanning their configured duration. Growth normally needs at least
three raw previews: one baseline and two comparable growth observations. Current
firing incidents remain firing during pending recovery. Recovery means the selected
condition stayed below threshold; a resolved growth incident does not mean the
bucket is unthrottled or the backlog is empty.

## Evidence and contributors

`RateEvidence` reports eligible/unknown status, reason, the observed preview,
growth availability/count/reason, deadline-evidence availability/reason, and a bounded contributor summary. Contributors
include shared bucket throttling, subscription pause, circuit state, configured
legacy pacing, saturated consumer/app/account capacity, active leases and scheduled
retries. They describe observed controls, not proof of a unique root cause. These
controls are not changed by monitoring.

Preview identity, revision, capacity bounds, risk-counter coverage, timestamps and
known constraint labels must validate. The watch requires complete pending coverage
with no pending-sample truncation. Missing data, stale/future/nonadvancing clocks,
configuration mismatch, unsupported servers and truncated pending samples produce
unknown/confirmation_lost rather than a successful resolution. Freshness and maximum
gap checks apply independently of the duration required for an alert.

The optional `deadline_coverage` marker, `deadline_recipients` count and
`earliest_deadline_at` support the approaching-deadline signal. Missing metadata
on older servers makes only that signal unknown; growth and modeled-risk evidence
can still be eligible. An explicit zero count proves no sampled effective deadlines.
The chosen deadline window is part of checkpoint condition identity.

Risk-detail truncation is separate: aggregate risk counts still cover the complete
pending sample, so valid aggregate counts can drive conditions when only the first
100 risk identities are returned. The preview exposes `risks_truncated` explicitly.
Neither a truncated pending count capped at 1000 nor missing evidence becomes zero.
Already expired identities remain facts as of the preview observation; the watcher
does not extend delivery deadlines or perform recovery actions.

Each new alert includes immutable `rate_constraints` context. Progress includes the
full bounded preview; the checkpoint alert does not copy event identities or
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
