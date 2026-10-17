# 1277: MCP readiness maintenance windows and notification silences

Status: Accepted

## Context

Disabling a readiness alert pauses incident observation and queued delivery.
Planned maintenance needs temporary notification muting while incident history
and deployment readiness remain observable. Muted backlogs must not replay as a
burst after maintenance, including after evaluator downtime.

## Decision

Store exact-environment, finite maintenance windows per owned app. Retain creator,
start/end, reason, creation time, cancellation account/time/reason. Immediate
silences use the same model with a start of now. Windows last at most 720 hours;
there is no recurring calendar in this change. History is bounded and paginated.
Changes require admin scope and MFA; reads use the existing read surface with
MFA. The app loader applies to every route. Dashboard mutations verify the named
MCP-alert CSRF envelope and delegate to the same owned-app handler. The durable
window ledger retains attribution independently of best-effort audit emission.

Continue observing every enabled rule during maintenance. Mark covered incident
events suppressed and retain matching window IDs and the suppression reason.
For mixed-environment rules every observed environment must be covered at the
same instant; a staging window cannot mute a production notification. Empty or
invalid coverage does not mute. Matching uses the full environment list in the
payload, independent of the bounded subject summaries. Queued work encountered
across a maintenance interval is suppressed; delivered events are unchanged.
Active leases may finish, and expired leases are fenced when suppressed.

Persist maintenance reconciliation on each incident. A fresh unhealthy
observation after the final covering window ends creates one continuation
`alert.fired` with the same incident ID, a new stable event ID, and
`notification_reason=maintenance_ended`. No new incident is manufactured. Remove
the former one-event-per-kind constraint for these continuation notifications;
a partial unique index preserves uniqueness of ordinary failure/recovery
transitions. Recovery still requires matching role/environment coverage.
Recovery after a wholly unattempted muted failure is suppressed, avoiding an
orphan recovery. Attempts that might have reached the receiver still warrant
recovery. Unavailable monitoring delays reconciliation, never implying health.

Window intervals are half-open and overlapping windows compose by union within
each environment. A cursor catches intervals crossed during evaluator downtime;
suppressed work stays terminal, so restarts cannot replay a burst. Cancellation
is idempotent and retains its original attribution. The PostgreSQL implementation
locks app, then rule, then events to serialize window edits, observations and
claims. Memory uses its existing mutex. Claim/acknowledgement token fencing and
at-least-once transport semantics from ADR-1548 remain in force.

The migration is replayable and refuses rollback when maintenance or suppressed
history would be lost. CLI, dashboard, OpenAPI and generated SDKs expose the same
window and notification status contracts. Apply the migration and upgrade all
readiness evaluators before creating windows: older evaluator binaries do not
recognize the terminal suppressed delivery state.

## Consequences

Planned work can mute notifications without erasing incident evidence. Delivery
already in flight can arrive during maintenance; no network send can be recalled.
Independent environment alert rules give more precise control than mixed rules.
Automatic expiry needs no external scheduler. Post-maintenance notification waits
for a fresh observation. Recurring schedules and paging escalation remain future
work.
