# ADR: Budget alerts on notification channels, and projected overruns

Date: 2026-10-19
Status: Accepted
Related: [ADR-566](566-financial-visibility-and-scoped-budgets.md), [ADR-749](749-alert-notification-channels.md), [ADR 20261019193600](20261019193600-ledger-dual-run-reconciliation.md)

## Context

A monitored budget (ADR-566) emails the account address once for each
threshold it crosses in a UTC month. That email is the only signal. Teams
that run on Slack or PagerDuty do not see a spending alert there, and a
finance contact who is not the account owner never receives one.

Alert rules already deliver to Slack, PagerDuty and email through
notification channels (ADR-749). Budgets cannot use them.

Every budget alert also arrives after the money is spent: a threshold fires
once known usage reaches it. `GET /v1/billing/forecast` already projects each
meter to the end of the month at its current rate, but no budget uses that
projection.

## Decision

1. **Binding.** A budget names up to `MaxChannelsPerFinancialBudget` (5) of
   its account's notification channels in `financial_budget_channels`. The
   binding is set with `PUT /v1/billing/budgets/{id}/channels`, read with
   `GET`, and from the CLI with `gregale billing budgets channels`. It is not
   part of the budget spec, so changing it creates no policy revision and does
   not re-send alerts already delivered. apid validates every id against the
   account's channels, as for alert rules.
2. **Additional, not instead.** The account email (ADR-566) is unchanged.
   Channels receive the same thresholds in addition to it.
3. **Durable per-channel delivery.** When meterd records a crossed threshold,
   it inserts one row per bound channel into
   `financial_budget_alert_channel_deliveries`, in the evaluation's
   transaction. meterd claims, sends and settles each row on its own, with the
   alert outbox's lease and fencing. A failure retries with the same backoff
   (1 minute doubling to 1 hour). After
   `FinancialBudgetChannelAttemptsMax` (12) claims, the row is `abandoned`. A
   channel never retries without limit on a configuration the customer must
   fix. Each channel's latest outcome is recorded on the channel, so `gregale
   channels list` and the budget's channel listing show it.
4. **Changes to bindings.** A channel bound later receives a threshold
   already crossed this month the next time the month is evaluated, once.
   Unbinding a channel holds its pending deliveries; binding it again resumes
   them. Deleting the channel, policy or alert deletes its rows. A disabled,
   deleted or edited policy holds its deliveries, as it holds the email.
5. **Message.** The text names the budget, the known usage, the threshold and
   the limit, preformatted from integer millicents, never through a float. It
   names the meters, basis and scope, says when coverage is partial, and says
   that the amount is the selected meters, not the invoice. On PagerDuty,
   every threshold of one budget in one month shares one incident (dedup key
   `budget-<policy>-<month>`). It is `warning` below the limit and `error` at
   it. Email channels use a key per alert and channel, so two channels never
   suppress each other.
6. **Where it runs.** Delivery uses the ADR-749 sender and the host identities
   held by meterd's alert evaluator. It runs on the budget delivery tick
   (`FinancialBudgetDeliveryInterval`), beside the email outbox. A meterd
   with no alert evaluator (no host identity and no Prometheus) leaves channel
   deliveries pending. The email outbox still runs.
7. **Projected overrun.** Each evaluation of a monitored budget also projects
   its amount to the end of the UTC month (`projected_millicents` on the
   evaluation). The projection uses each selected meter's run-rate forecast:
   - Gross usage scales the scope's gross amount by the forecast's
     projected-to-observed quantity.
   - Net usage takes the account's projected net charge, in the scope's share
     of the observed quantity. This is how the ledger allocates a net charge.

   There is no projection (`projection_reason` says why) when any selected
   meter has no forecast, or the evidence is incomplete or stale.
   `ForecastMeter` also needs at least a day of uninterrupted coverage, so a
   quiet month start never projects.

   When the projection reaches the limit while known usage is still below it,
   meterd raises one projected-overrun alert for that policy revision and
   month. It goes to the account email and the bound channels, through the
   same outboxes, leases and retries. It is a `warning` and shares the budget
   month's PagerDuty incident. It is an estimate, not a decision: it never
   stops a workload, even on a `stop_previews` budget.
8. **Storage of projections.** Projected-overrun alerts live in their own
   table, `financial_budget_projection_alerts`, with its own email outbox.
   `financial_budget_alerts` keys an alert by policy, revision, month and
   threshold, and the previous release inserts into it. A projection at the
   limit would take the limit alert's key, so the real limit alert would be
   dropped. Channel deliveries name their source with `kind` (`threshold` or
   `projection`) and cascade from the policy.

## Consequences

- Three tables, added with an expand migration:
  `financial_budget_channels` (configuration),
  `financial_budget_projection_alerts` and
  `financial_budget_alert_channel_deliveries` (operational).
- `GET /v1/billing/budgets/{id}` reports `runtime.evaluation.projected_millicents`
  and `runtime.projection`. `gregale billing budgets get` prints the
  projected month end.
- A linear run rate overstates a month front-loaded by a one-off burst and
  understates one that grows. The alert says it is an estimate, and fires at
  most once a month per revision.
- `meterd_alert_channel_deliveries_total{kind,outcome}` now also counts
  budget deliveries. No new metric.
- Strict budgets have no thresholds (ADR-1750), so a channel bound to one
  receives nothing. The binding is allowed, so that it applies if the policy
  later becomes monitored.
- Rejected:
  - **Channel ids inside the budget spec.** Every channel change would become
    a revision. A revision re-keys alerts, so every crossed threshold would
    be re-sent to every destination.
  - **Fan-out inside the email claim.** One failing channel would block the
    email and the other channels, and a retry would resend to destinations
    that had already accepted.
  - **Unlimited retries.** A deleted Slack webhook would retry until the end
    of the month.
  - **A projection as a row in `financial_budget_alerts`.** It would share the
    limit alert's unique key. Changing that key needs a contract migration
    and breaks the previous release's `ON CONFLICT` insert.
  - **Run-rate on the budget's net amount.** Net usage stays at zero until the
    allowance is used, and then grows faster than linear. Only the quantity
    is linear.
