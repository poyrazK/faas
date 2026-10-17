# ADR-1500: Budget threshold notifications

Status: accepted.

Amends the ADR-566 preview release scope
([financial-controls-implementation.md](../financial-controls-implementation.md)),
which refuses every budget activation.

## Context

ADR-566 shipped budgets as disabled drafts: a customer can preview what a
budget would cover, but `enabled: true` is refused with
`financial_budget_activation_unavailable`. Its full design (holds, drains,
strict reservations) changes `schedd` and gateway behaviour and needs native
lifecycle acceptance, so it remains future work.

Before public launch, customers still need to hear when their spending passes
an amount they chose. Notification is the one budget response that changes no
workload. It needs only meterd's existing financial evidence and an email.

## Decision

1. **Activation.** A budget with `mode: monitored` and `action: notify` can be
   enabled. Every other combination is still refused with
   `financial_budget_activation_unavailable`. An enabled notify budget reports
   `status: active`, `enforcement_ready: true` and no reasons, because its
   selected response is fully integrated. Drafts keep `status: draft`.
2. **Evaluation.** Every `FAAS_BUDGET_NOTIFY_INTERVAL` (default 5 minutes)
   meterd lists the active notify budgets. For each account that has one, it
   reads the current UTC month's financial costs once. It then computes each
   budget's known amount the same way the preview does (`BudgetAmount` over
   the retained allocations). The thresholds are `notify_millicents` plus
   `limit_millicents`. The known amount is a lower bound: incomplete or
   delayed evidence can only make a notification late, never send one early.
3. **Once per threshold and month.** Before sending, meterd inserts a
   `financial_budget_notifications` row for each crossed threshold, keyed by
   (budget, UTC month, threshold amount). An insert that conflicts with an
   existing row is a no-op, so a threshold notifies at most once a month.
   That holds across restarts and across concurrent meterd processes. Editing
   a budget keeps the rows for thresholds already crossed; a new threshold
   amount is a new row. A new month starts with no rows.
4. **Delivery.** One email goes to the account's address for each budget with
   pending rows. It names the highest newly crossed threshold, and every
   pending row of that budget and month is marked sent with it. The mail
   provider's idempotency key is derived from the budget, month and that
   threshold. A failed send is retried on later passes, up to
   `FinancialBudgetNotifyMaxAttempts` (`pkg/api/limits.go`); after that the
   rows are marked `abandoned` with the last error. If a budget is disabled
   or deleted before delivery, its pending rows are marked `cancelled`
   without sending. A crash between a successful send and recording it can
   repeat one email (at-least-once).
5. **Scope of the amount.** Budgets cover the usage meters they name
   (compute, egress), net or gross as configured. Plan fees, credits and
   taxes are not part of the amount, as in ADR-566.

## Consequences

- meterd gains one timer and `meterd_financial_budget_notifications_total`
  (labelled by outcome: `sent`, `failed`, `abandoned`, `cancelled`).
- Stopping actions, strict budgets, holds and budget webhooks remain
  ADR-566 future work. A notify budget never stops a workload.
- The account address is the only recipient. Team recipients and alert
  channels (webhooks, Slack) are not part of this decision.
- Rejected: one email per crossed threshold (a budget activated mid-month
  above three thresholds would send three emails at once); evaluating from
  sampled request costs (ADR-566 rejects analytical estimates as financial
  evidence); sending without a durable claim (a restart would repeat every
  notification).
