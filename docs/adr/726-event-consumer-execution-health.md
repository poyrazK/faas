# ADR-726: Consumer execution health and alerts

- **Status:** accepted
- **Date:** 2026-10-08
- **Decision:** Add an execution-health observation beside routing health, using retained delivery roots, invocation lineage, and attempt history.
- **Why:** Successful routing admission can conceal repeated handler failures and dead-letter accumulation.

## Observation and attribution

`GET /v1/apps/{slug}/event-subscriptions/{subscriptionID}/execution-health`
uses the existing read scopes, MFA, account/app ownership, bounded request
timeout, and windows `5m`, `15m`, `1h`, `6h`, and `24h`. The CLI command is
`events subscription-execution-health`. Routing health stays independent.

Select the newest 1,000 retained admitted application recipient roots, including
settled publications and materialized historical backfills. Use the stable
invocation identity from the accepted envelope and subscription, then read up
to 5,000 retained root and replay invocations owned by that account and app.
Replay attribution uses durable replay-root metadata, with immediate-parent
fallback for legacy rows. Multiple invocations and replay generations are not
unique event counts. PostgreSQL uses sqlc queries and a read-only repeatable-read
transaction. No invocation, receipt, or attempt checkpoint is changed.

Current counts separate queued (`pending`, no dispatch attempt in the current
generation), retrying (`pending`, attempts greater than zero), running
(`dispatching`, including admission/wake waits), succeeded, failed, expired,
dead-lettered, cancelled, superseded, and unknown states. These counts cover
retained executions across acceptance times, rather than only the selected window.

## Windowed measures

Count retained completed attempt outcomes in the selected window. Handler failure
percentage is `(retry + failed + dead_letter) / (succeeded + retry + failed +
dead_letter)`. Cancelled and unknown outcomes are outside that denominator;
unknown attempts are reported separately. The dead-letter rate counts retained
`dead_letter` attempt outcomes per window second, so a replay can contribute
another outcome. This is a recorded formation rate, not net backlog growth.

Successful completion p95 uses continuous interpolation between original event
acceptance and the latest retained successful completion of each linked invocation
in the window. It includes backfill age, routing, queue waits, handler retries,
and replay delay. In-place replay only has its current invocation completion;
completed replay children contribute their own observations.

## Retention and bounds

Coverage is `bounded_retained_execution_roots`; `history_complete` is always false.
Pruned receipts cannot attribute remaining invocations. Pruned invocations and
attempt history cannot be reconstructed. `retained_roots` counts selected admitted
roots; `missing_roots` identifies original invocation rows absent from the sampled
execution rows. `truncated` marks the root or linked-invocation bound. Missing roots
may still have retained children and attempts. All windowed measures describe
retained observations and do not certify a complete historical failure rate.

## Alerts

Add webhook-only subscription alerts:

- `event_execution_dead_letters`: current retained dead-letter invocation count.
- `event_execution_dead_letter_rate_per_second`: retained dead-letter attempt outcomes per second.
- `event_handler_failure_pct`: retained handler attempt failure percentage.
- `event_completion_latency_p95_seconds`: acceptance-to-completion p95.

Missing roots, truncation, unknown current states, or no retained roots produce
`unknown`. Attempt-based alerts also reject unknown attempts. Failure percentage
needs 20 completed success/failure observations; latency needs one success.
A routing pause does not suppress execution alerts: already admitted work can
still execute and fail. These observations do not create per-subscription
Prometheus labels. A constraint-only migration permits the new metrics using
the existing subscription selector; rollback requires removal of those rules.

Cancellation-only event work has a durable cancellation proof instead of a
handler invocation. Execution health excludes these actions from its roots;
its cancelled count describes invocation cancellation, not cancellation commands.
