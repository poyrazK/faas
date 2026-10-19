# ADR-1401 · Read-only health and sustained alerts for application event outboxes

Date: 2026-10-15
Status: Accepted

## Context

Applications can stage, recover, reconcile and resume publication intent, but
operators need aggregate evidence of stalled recovery and blocked or exhausted
work. Scheduled retries and live claims must not be reported as stalls. Partial
or stale evidence must not establish recovery.

## Decision

Add owner-scoped Go SDK summaries over a bounded sample of retained unpublished
outbox rows. Use one statement snapshot and database clock, exclude published
history, and expose completeness, observation time, counts and eligibility ages.
Keep expiry distinct from stored blocked state and active recovery eligibility.
Read metadata only, with sanitized errors and no publication or control effects.

Provide optional local watches for sustained due backlog, stalls, blocked rows and
expired publications. Reuse the acknowledgment watch's condition state machine
and metadata evidence checks. Require complete, fresh, advancing observations for
firing and resolution, and treat gaps or invalid evidence as lost confirmation.

Extend opt-in application DDL with an unpublished-row health index. SQL uses the
existing outbox ADR's static SQLC validation exception; no platform Store,
migration, endpoint, or clone registry entry is introduced.

## Consequences

Applications gain visibility without an automatic recovery or notification
service. Bounds can make coverage incomplete on large backlogs; clock skew,
slow callbacks, or read failures can invalidate alert evidence. Watches persist
no incident state. Resolution concerns a selected count, not proof of publication
or consumer completion. Applications own retention, process lifetime and any
external or durable notification transport.

See [usage and exact semantics](../event-outbox-health.md).
