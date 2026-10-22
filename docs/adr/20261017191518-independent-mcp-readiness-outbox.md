# 1276: Independent MCP readiness incidents and delivery outbox

Status: Accepted. Maintenance suppression and continuation notifications are
extended by [ADR-20261017191519](20261017191519-mcp-readiness-maintenance-windows.md).

## Context

Using the newest delivery attempt as the incident cursor made destination
availability determine which incident transitions could be retained. A failure,
recovery, and second failure during an outage could collapse into one incident.

## Decision

Persist incident state, immutable logical events, and the per-rule observation
cursor independently of the transport journal. Recording an observation locks
its rule and atomically changes incident state, appends any new event, and
queues that event for delivery. The cursor rejects older or equal observation
timestamps; configuration changes invalidate an in-flight observation. Complete
role coverage remains required for recovery. Notification success is irrelevant
to the decision. Readiness observations run before readiness dispatch in each
evaluator pass.

The oldest undelivered event is the only eligible claim per rule. Claims create
ordinary alert-delivery rows for transport diagnostics and lease the logical
event for two minutes with a fresh token. Dispatch is bounded to one minute.
Completion checks the current unexpired token, and failed or incomplete attempts
retry after cooldown. An expired lease is reclaimable without changing the
logical event ID. Queue ordering affects notification timing only; later incident
facts continue to be recorded. Delivery is at least once, so receivers dedupe by
the stable signed event ID.

The owned-app timeline now has one row per logical event and uses occurrence
time plus event ID for keyset pagination. Current incident status and notification
status are separate, with attempt count, last attempt, retry eligibility, and a
link to the last transport row. CLI, dashboard, OpenAPI, and SDKs share this
contract. Retry attempts cannot change the timeline page boundaries.

Migration imports existing lifecycle events, preserving IDs, logical occurrence
time, latest transport status, and attempt counts. Replay preserves live queue
state. Rollback refuses to discard independently recorded events or active
leases. Incident history survives transport-journal cleanup; rule deletion
cascades to its incident records, events, cursor, and queue. Enablement pauses
observation and future delivery claims.

## Consequences

Notification outages no longer suppress failure/recovery history. Slow or failed
destinations still delay later notifications for their rule. Leases fence late
worker completions; a crash after receiver acceptance can still require a
receiver-deduplicated replay. Monitoring source gaps do not manufacture recovery.
No deployment or traffic action is introduced.
