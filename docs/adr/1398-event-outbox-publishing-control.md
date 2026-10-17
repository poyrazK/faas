# ADR-1398 · Durable scope pause for application outbox recovery

Date: 2026-10-15
Status: Accepted

## Context

Operators need to suspend new publication during incidents without stopping every
worker or mistaking an intentional pause for failed recovery. The pause must not
race with claim commit, cancel already committed deliveries, or extend retention.

## Decision

Add application-owned account/app scope state and independent command receipt
tables. Require revision guards and command IDs; commit state, history sequence
and typed metadata receipt atomically. Serialize commands with an exclusive
transaction advisory lock and claims with its shared counterpart. Read pause
state after acquiring the claim lock under Read Committed isolation. Do not hold
scope locks through HTTP.

Expose current state, replayable command lookup and bounded history pagination.
A missing row is initially enabled; unavailable storage fails closed. Preserve
attempts, schedules, keys, per-row policies and deadlines. Already committed claims
may finish. Worker pools idle on pause and emit local observations.

Include scope state and PausedDue in health snapshots. Suppress due/stall
confirmation during a fresh complete pause rather than resolving incidents;
blocked/expired watches continue. Upgrade all older workers/readers before relying
on pause semantics. The DDL is opt-in application storage, using the existing
outbox static SQLC validation exception, not a platform migration or new endpoint.

## Consequences

Pause persists across worker restarts and publication pruning. Applications must
retain the authoritative scope row and command evidence, bound lock waits with
contexts, and coordinate SDK/schema rollout. Commands are metadata history, not
independent actor authentication. Pause is not quiescence, forced HTTP cancellation,
or an extension of deduplication/retention. Existing consumer recovery and ordering
contracts remain unchanged.

See [usage, fencing and retention](../event-outbox-publishing-control.md).
