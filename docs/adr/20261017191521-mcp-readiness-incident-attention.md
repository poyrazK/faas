# 1279: Incident acknowledgement and finite readiness reminders

Status: Accepted

## Context

Readiness incidents and the durable notification queue survive receiver outages,
but an operator cannot record ownership of an open incident or request bounded
follow-up notifications while it stays unhealthy.

## Decision

Add per-incident acknowledgement and opt-in reminders. Neither changes readiness,
resolves an incident, moves traffic, nor cancels the initial failure, recovery or
maintenance-end notification. Acknowledgements retain account, reason, request ID,
time and optional absolute expiry (future, at most 30 days); omission is indefinite.
An operator can replace or release one. Disabled readiness webhook rules can be
configured but cannot observe, create or dispatch reminders.

Require admin scope, MFA, an owned app, a current attention version, a stable
app-scoped request UUID and a nonblank reason of at most 500 UTF-8 bytes. Serialize
by app then rule with the evaluator and queue dispatcher. Save the JSON snapshot
and immutable decision receipt in one transaction. Identical canonical retries
return their original receipt even after recovery or expiry; a changed request
conflicts. Reads require read scope and MFA. Browser writes also require the
existing named MCP-alert CSRF envelope. API, CLI, dashboard and generated SDKs
share the contract. Rule deletion cascades attention records with incident history.

Reminders default off. Enable a 60–86400 second interval and a lifetime limit of
1–10 created reminders for that incident. Both zero disables. Policy changes,
acknowledgement, suppression and abandonment never reset the count. A new incident
has a new budget. Each creation increments attention version; routine evidence
updates do not invalidate operator decisions. Release and reconfiguration anchor
next eligibility to their decision time plus interval. Normal failure or
maintenance-end creation anchors it at least one interval after that notification.

Create at most one reminder inside a fresh evaluator observation transaction,
only for the open incident's exact coverage, an unhealthy result, no active
acknowledgement or maintenance, available budget, elapsed interval, and an empty
owned rule queue. Equal/older observations are ignored by the existing cursor.
No wall-clock scheduler manufactures missing evidence, and downtime never creates
a catch-up burst. Reminder events are `alert.fired` with `notification_reason:
reminder` and a 1-based `reminder_number`. Their IDs derive from incident UUID and
creation number; payload and ID remain stable across delivery retries.

Before dispatch and after observations/decisions, suppress unleased or expired-lease
reminders when acknowledged, resolved, disabled by policy, over a reduced budget,
or lacking matching unhealthy evidence no older than two minutes. Suppression is
terminal and advances delivery version. Future-dated events and live leases are
left alone; in-flight HTTP may finish. Ordinary failure/recovery transport remains
unchanged. Monitoring outages cannot authorize stale reminders. Maintenance
retains its own suppression and continuation semantics. The lifetime budget bounds
logical reminders, not transport retry attempts.

Persist snapshots and receipts in new tables and add the reminder number plus a
unique per-incident number index to the existing outbox. Rollback refuses to drop
any decision or reminder history. Migrate and upgrade **all readiness evaluators**
before enabling reminders; older binaries do not enforce acknowledgement or stale
reminder suppression. Native paging, fallback destinations, rule-default reminder
policies and automatic escalation remain future work.

## Consequences

Operators can record attention without claiming service recovery. Finite reminders
reuse signed, at-least-once webhook delivery, queue inspection and explicit recovery
controls. Receivers still deduplicate stable event IDs. A reminder may be suppressed
and consume budget without ever reaching a receiver. Read-only attention/history
views do not mutate schedules; eligibility times are not delivery guarantees.
