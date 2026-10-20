# ADR-20261016123221 · Customer Operations telemetry

- **Status:** accepted for implementation
- **Date:** 2026-10-16
- **Owner:** Operations maintainers
- **Related:** [ADR-521](521-customer-operations.md),
  [ADR-1196](1196-operation-deadlines-and-budgets.md),
  [ADR-20261016100929](20261016100929-webhook-delivered-operation-signals.md),
  [ADR-1235](1235-spec12-operator-alerts-at-warn.md), [ADR-1236](1236-paging-set.md)

## Context

Customer Operations have no Prometheus series of their own. Operators can see
invocations and workflow steps, but not how many Operations are waiting,
whether deadlines are enforced, or whether the platform is losing attempts and
pushing Operations into reconciliation. Admission cannot leave preview without
that evidence.

Operation state lives in Postgres, and transitions commit in both apid and
schedd, often inside transactions that can still roll back. Counting
transitions in process would need post-commit hooks in two daemons and would
double count retried transactions.

## Decision

**Fleet gauges from Postgres (schedd).** Each schedd refreshes one snapshot of
fleet-wide Operation state in its existing 60-second retention tick and serves
it from a collector, so every scrape sees one consistent snapshot and no stale
series. Every schedd reports the same fleet values; rules and panels aggregate
with `max`.

| Series | Labels | Meaning |
|---|---|---|
| `schedd_customer_operations` | `state` | Operations `accepted`, `running`, `waiting` or `requires_reconciliation` |
| `schedd_customer_operations_waiting` | `kind` | Waiting Operations by `timer`, `approval` or `event` |
| `schedd_customer_operations_oldest_update_seconds` | `state` | Time since the least recently changed Operation in that state changed |
| `schedd_customer_operations_overdue` | — | Active Operations more than three sweeps past their deadline |
| `schedd_customer_operations_settled_recent` | `outcome`, `failure_code` | Outcomes in the last 15 minutes |
| `schedd_customer_operations_outcome_unknown_accounts` | — | Accounts with an `execution_outcome_unknown` reconciliation in the last 15 minutes |
| `schedd_customer_operations_telemetry_refreshed_timestamp_seconds` | — | When the snapshot was last read |

`failure_code` is a closed set: the codes Operations settle with today, `none`
and `other`. The active gauges read the partial index of pending Operations.
The outcome gauges read outcome events through a new partial index on
`customer_operation_events (created_at)` for the four outcome event types.

**Webhook outcomes (apid).** `apid_customer_operation_webhook_events_total`
counts verified events on operation-bound endpoints by `mode` (`start`,
`signal`) and `result`: `accepted`, `duplicate`, `ignored`, `signaled`,
`unmatched` or `refused` (any non-2xx answer).

**Alerts.** Both are `warn`; neither pages (ADR-1235, ADR-1236).

- `FaasCustomerOperationDeadlinesOverdue`: an active Operation stayed more than
  three minutes past its deadline for ten minutes. The deadline sweep or the
  invocation deadline reaper has stopped.
- `FaasCustomerOperationOutcomesUnknown`: three or more accounts had an attempt
  end with an unknown outcome in fifteen minutes. One app timing out is its
  owner's problem; several accounts at once point at the platform losing
  attempts.

A Grafana dashboard shows the gauges and the webhook rates.

## Consequences

- Operators can see Operation volume, waits, reconciliation backlog and
  outcomes without querying Postgres.
- The gauges lag by up to one tick, and the outcome window is fifteen minutes,
  not a counter; rates come from the window, not `rate()`.
- One more partial index on the events table; outcome events are a small share
  of its inserts.

## Rejected alternatives

- **In-process transition counters.** Two daemons, rollbacks and retried
  transactions make them wrong.
- **Per-account or per-app labels.** Unbounded cardinality; the Operations API
  already answers per-account questions.
- **Alerting on reconciliation backlog.** Reconciliation is the customer's
  decision; a backlog says nothing about platform health.
