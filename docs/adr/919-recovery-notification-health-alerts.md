# ADR-919: Recovery notification delivery health and alerts

Date: 2026-10-09
Status: Accepted

## Context

A recovery admission or execution-finished event can select several webhook receivers. Capturing the event does not establish acknowledgement, and independent delivery failures need app-scoped visibility. Retained history can be incomplete.

## Decision

Extend recovery health with independent admission and execution notification counts for overdue, dead, unknown, and no-receiver jobs. Overdue requires at least one known unacknowledged receiver and a capture timestamp at least 15 minutes old. Dead and overdue may overlap. Unknown evidence never proves failure or successful acknowledgement. Empty frozen selections are explicitly no-receivers; their alert rules are opt-in, as are all new rules.

Inspect at most 50 candidate terminal jobs in admission-completion order, with ID as tie-breaker, and return at most three diagnostic notices. Exclude only selections with complete retained successful delivery evidence. An extra candidate marks both phases incomplete; unknown evidence marks its phase incomplete. Candidate-set completeness and phase evidence completeness are distinct. Bounds limit returned observations, not the database work needed to find candidates. The existing five-second health request budget remains shared across sections.

Use the existing notification report observation code in the same read-only repeatable-read PostgreSQL transaction, or under the existing memory-store lock. Reads never capture, relay, retry, or alter jobs. Historical jobs can remain unknown until pruning; no timestamps are inferred from admission completion.

Add eight app-scoped webhook alert metrics under `event_recovery_notification_{admission,execution}_{overdue,dead,unknown,no_receivers}_jobs`. Existing recovery-health rule windows and validation apply. Incomplete observations can only trigger satisfied `gt`/`gte` lower bounds. They cannot clear an alert or send recovery notifications; other comparisons degrade. All counts are instantaneous retained job observations, not counts accumulated over the rule window.

## Consequences

The CLI and generated SDKs expose the same evidence and completeness flags. Operators can open the existing notification report for receiver details and independently retry dead deliveries. Missing or pruned delivery evidence can block recovery evaluation until a complete observation is available. No rule is created automatically.

Apply migration `20261009225025229_event_recovery_notification_health.sql` before deploying API or alert-evaluator binaries. It expands alert constraints and adds a terminal-job lookup index. Before downgrade, roll back binaries and remove all eight new metric rules; the down migration refuses to silently discard them.
