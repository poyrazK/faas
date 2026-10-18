# ADR-1389 · Read-only health for application-owned acknowledgment recovery

Date: 2026-10-15
Status: Accepted

## Context

Durable acknowledgment intents preserve callback recovery across crashes, but
operators need to distinguish work waiting intentionally from work no longer
being recovered. Business completion and callback confirmation remain separate.
The SDK cannot assume an application's notification transport or process lifetime.

## Decision

Add owner-scoped Go SDK health reads over retained unfinished intents and an
optional in-process sustained-condition watch. Bound each read, expose truncation
and invalid metadata, and use the database clock to classify eligibility. Exclude
live claims and future schedules from stalled counts. Return metadata only.

Require complete, fresh, advancing observations for firing and recovery. Lost
proof becomes unknown or confirmation lost, never a successful resolution. Keep
polls sequential, queries time bounded, thresholds opt-in, and notifications local.

## Consequences

Applications gain backlog and stall visibility without mutating recovery state or
introducing server schema changes. Large backlogs can exceed the configured scan
bound and suspend alert confirmation. Clock skew, delayed callbacks, and gaps can
also invalidate evidence. Applications own transport, durable alert identity,
retention, and restart behavior. This health signal does not prove business
execution success or change ordering and deduplication guarantees.

See [usage and semantics](../event-acknowledgment-intent-health.md).
