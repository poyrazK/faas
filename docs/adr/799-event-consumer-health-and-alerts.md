# ADR-799: Consumer routing health and alerts

- **Status:** accepted
- **Date:** 2026-10-08
- **Decision:** Expose app subscription health from durable backlog and retained routing outcomes, and evaluate consumer alerts in the existing alert service.
- **Why:** An independent consumer can accumulate retries or backlog while sibling consumers remain healthy. Operators need consumer-specific signals and maintenance suppression.

## Observations

`GET /v1/apps/{slug}/event-subscriptions/{subscriptionID}/health` returns live
pending and processing counts, oldest pending age, continuous pause duration,
and recorded outcomes for a 5m, 15m, 1h, 6h, or 24h window. PostgreSQL reads
controls, backlog, and history in one repeatable-read transaction. The consumer
must belong to the account and app, either as a current subscription or retained
control. App deletion makes the endpoint unavailable.

Routing success is an `enqueued` fanout or application backfill outcome, before
invocation execution. Drain rate is successful recorded routes divided by
window seconds. Scheduled retry rate counts `pending` outcomes with a recorded
error and no capacity deferral. Maintenance and paced-admission waits do not
count. Terminal failure percentage divides failed outcomes by failed plus
enqueued outcomes; filtered events, operator replay actions, and pending waits
are excluded. Recovery can produce several outcomes for one recipient.

P95 latency uses continuous interpolation over successful routing outcomes,
from original event acceptance to admission. Historical backfill therefore
includes time spent waiting before backfill was requested. These metrics do not
measure invocation completion, execution retries, or unique event totals.

History is bounded by existing retention and compaction. Coverage is explicitly
`bounded_recorded_outcomes`; `history_compacted` marks a window intersecting
compacted records. Rates are retained observations, not lifetime counters.
No subscription labels are added to Prometheus; consumer observations and
alert evaluation use account-scoped stored reads, avoiding unbounded label
cardinality. Window reads have a consumer/time index and API request timeout.

## Alert rules

The immutable `event_subscription_id` selector is required only for consumer
metrics. Rules remain app-scoped, use the existing signed webhook outbox,
cooldown, and delivery audit, and permit webhook actions only. The supported
metrics are pending recipients, oldest pending seconds, scheduled retry rate,
terminal failure percentage, routing p95 seconds, paused seconds, and drain rate.
Creation verifies ownership; metric family changes require delete/recreate.
Consumer rules use windows up to 24h.

Paused consumers skip all health alerts except `event_paused_seconds`, setting
rule state to `unknown` and stamping evaluation time. Rate and latency alerts
also skip compacted windows. Failure percentage requires 20 terminal observations;
latency requires at least one successful route. Drain-rate rules require an active
backlog to avoid firing on idle consumers. Pending counts and pause time
remain usable regardless of retained history. Existing queued notifications and
admitted invocations keep their lifecycle.

A stable `paused_at` records the first pause transition; repeated pauses preserve
it, and resume clears it. Migration backfills existing paused controls from
`updated_at`. Both health and controls survive worker restarts. Operational
controls and alert rule state are excluded from environment cloning.
