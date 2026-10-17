# Full pending-recipient summaries

`GET /v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-control/pending-summary`
returns read-only counts and effective deadlines across **all retained captured
pending application recipients** for one owned subscription. It requires the
existing read scope and MFA policy, sends `Cache-Control: no-store`, and returns no
payloads or recipient identities. Go clients expose
`GetEventSubscriptionPendingSummary`; generated Node and Python clients expose the
same operation.

The summary includes `pending_recipients`, `deadline_recipients`,
`already_expired_count`, and optional `earliest_deadline_at`. Zero deadlines means
an explicit zero count with no earliest timestamp. Effective deadlines use the
same captured delivery-age policy and replay overrides as delivery admission and
rate previews. Expired pending rows are included until their routing state changes.
Processing deliveries, publications awaiting recipient capture, future arrivals,
other subscriptions and historical purged data are outside the coverage.

## Snapshot and budgets

Production reads use a read-only Repeatable Read transaction, one database clock,
and current subscription token capacity, revision, pause, legacy rate, circuit and
live-capacity controls from that snapshot. The in-memory store reads under one
lock. Neither path claims work, consumes tokens, allocates receipts or changes
configuration. No migration is required.

There is no 1,000-row sampling cap, pagination or count extrapolation. The database
aggregates the scoped pending set without returning individual rows. The request
budget is five seconds; a transaction-local four-second statement timeout also
bounds database statements. The budget applies to direct store calls as well as
HTTP requests. It limits read time, not a guaranteed maximum backlog size or query
cost. Under pressure, queries can time out rather than produce a usable summary.

A timeout returns HTTP 504 without partial aggregates. Unusable clocks return
`complete: false` with `clock_uncertain`; those counts cannot prove health or
recovery. Counts are bounded to exact JSON integers; representation overflow fails
the read. Missing, stale, nonadvancing, incomplete or failed evidence remains
unknown. Never substitute zero after a failure.

## Monitoring

The Go delivery-health watch uses this summary for opt-in `RateRisk.ThrottledGrowth`
and `RateRisk.DeadlineWithin` conditions. Their metric names begin
`pending_summary.`. They remain usable above 1,000 pending recipients when the
full scan completes within its budgets. Growth compares complete consecutive
observations with the same rate policy, while the token bucket is rate limited at
both observations. It is net count growth per observation interval, not an arrival
rate or proof that every intervening instant was throttled.

`RateRisk.DeadlineRisk` continues using the sampled rate-only model. That condition
remains unknown when pending rows exceed the preview's sampling cap. A failed or
truncated model does not invalidate an otherwise eligible summary. Progress exposes
summary and model coverage separately. When modeled-risk monitoring is enabled,
its preview follows the summary and checks the same bucket revision/settings;
they are separate snapshots, not one atomic combined observation.

Summary metrics use distinct condition identities from the earlier sampled growth
and deadline-window metrics. Checkpoints containing those sampled metric names
cannot resume as full-summary conditions. Preserve and settle their pending alerts
using the earlier compatible client and start a new monitoring session. Existing
modeled-risk metric identity and acknowledgement behavior are unchanged. On older
servers without this endpoint, the new summary-based monitoring stays unknown;
there is no silent fallback to sampled counts.

The summary is neither a completion estimate nor a deadline-safety certificate.
Approaching-deadline warnings include paused, capacity-blocked and leased pending
rows independently of modeled admission slots. Controls can change immediately;
contributor labels describe observed conditions and do not establish a unique root
cause. Monitoring performs no recovery action and extends no delivery deadline.

See [watch configuration](event-delivery-rate-risk-alerts.md) and
[the sampled pacing preview](event-subscription-rate-preview.md).

To identify individual pending deliveries by effective deadline, use the
[deadline triage listing](event-subscription-deadline-triage.md). It supports
absolute bounds and live pagination independently of the preview sample cap.

For complete upcoming counts and separate expired waiting-reason partitions, use
[deadline-window monitoring](event-subscription-deadline-window.md). It adds optional
sustained count alerts and matching relative-window/reason triage filters.
