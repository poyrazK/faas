# 1278: Readiness notification queue health and operator recovery

Status: Accepted

## Context

The independent readiness outbox records incidents throughout destination outages,
but FIFO delivery means a permanently failing head delays every later notification
for its rule. Individual attempt rows cannot summarize the queue or provide a safe
operator decision after correcting a receiver.

## Decision

Expose a bounded per-app queue snapshot ordered by rule UUID, with outstanding
count, count behind the head, oldest age, head event, safe failure classification,
retry eligibility and paused/maintenance/leased states. A scoped query includes
all-environment rules and their entire queues; filtering away a blocking head
would incorrectly suggest delivery could advance. PostgreSQL reads use one
repeatable-read transaction. Reads never perform suppression or network dispatch.
Remote errors, webhook URLs, credentials and response bodies are excluded.

Add an optimistic delivery version to each logical event. Increment it on claims,
completion, suppression and operator decisions. Admin scope and MFA are required
for recovery; the owned-app loader applies and dashboard actions require the
existing named MCP-alert CSRF envelope and a live authenticated browser session.

An operator can retry an attempted failed/expired-lease head, making it eligible
on the next evaluator pass without changing its logical event ID or cooldown
configuration. An operator can abandon the head permanently, retaining its event,
attempts and incident state. Later events, including recovery, can then proceed.
Abandoning an unattempted failure can intentionally yield an orphan recovery; the
UI and API describe that consequence. There is no automatic abandonment budget.
Delivered, suppressed and abandoned events are terminal and cannot be revived.
Maintenance reconciliation cannot rewrite an abandoned event.

Both actions require the current version, a UUID request ID and a nonblank reason
of at most 500 UTF-8 bytes. They refuse non-head events, disabled/ineligible rules,
future events, live leases and any event that maintenance reconciliation would
suppress, including historical windows crossed during downtime. An expired lease
can be cleared; late completions remain fenced by their token. No action sends a
webhook inline. Retry eligibility is a hint, not a delivery guarantee.

Serialize decisions by app, rule and event, matching observation/claim/window
lock order. Save the queue mutation and immutable receipt in one transaction.
The app-scoped request key returns the same receipt for the same canonical request
even after delivery, suppression, rule disablement or another action; changing
its event, version, action or reason conflicts. Receipts and last-action metadata
retain original actor, time, reason and resulting version independently of
best-effort audit emission. Rule deletion cascades receipts along with its
existing incident history. App ownership is checked before receipt lookup.

The migration is replayable and guards rollback when recovery history exists.
Deploy the migration and upgrade all readiness evaluators before enabling these
controls: older evaluators neither increment delivery versions nor recognize
abandoned terminal events. API, CLI, dashboard, OpenAPI and generated SDKs share
the contract. No recurring calendar or paging escalation is added.

## Consequences

Operators can diagnose notification delays and explicitly unblock delivery while
retaining service incident evidence. At-least-once delivery still permits an
expired attempt to have reached a receiver; receivers must deduplicate stable
event IDs. An abandonment receipt stops future attempts, not an HTTP request
already accepted by a receiver. Queue pagination is stable by rule identity, but
each subsequent page is a new snapshot and can reflect intervening changes.
