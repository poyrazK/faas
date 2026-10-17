# ADR-1488: One-step backup team escalation for tracker incidents

Date: 2026-10-15

Status: Accepted

## Context

Bounded reminders reach the current owner but do not transfer notification
responsibility when nobody acknowledges an incident. Owners need one optional
backup team, without introducing on-call schedules or provider lifecycle actions.

## Decision

Add a separately versioned, owner-only escalation policy per app. It is disabled
by default and selects an app-scoped team and a delay of 15 minutes through seven
days (24 hours by default). Saving it never changes incident identity, reminder
budget, triage revision or notification policy revision. Require enabled current
notification and reminder policies and an existing first reminder. The due time
is one delay after the latest of first reminder, escalation policy save or snooze
expiry. Later reminders do not postpone escalation. Manual unsnooze can make an
already overdue incident eligible at the next sweep.

Persist first-reminder time and a single escalation receipt on the incident.
Preserve both across automatic checks of the same identity, reset when identity
changes, and backfill existing first-reminder time from the last reminder receipt
so migration cannot trigger premature escalation. Retain the configured team UUID
without a foreign key so deleted teams cannot erase the receipt or trigger replay.
Settings must initially reference an existing team in the same app; deletion or
loss of all eligible members causes app-owner fallback at enqueue.

Use the existing bounded one-minute apid sweep and app row lock with SKIP LOCKED.
Recheck app/owner/plan/abuse eligibility, incident identity and settings revision,
acknowledgment, snooze and policy opt-ins under the app lock. Queue one deterministic
incident/kind notice and its webhook outbox event, capture eligible recipients and
persist the escalation receipt atomically. Outbox failure rolls everything back;
concurrency, restarts, retention and policy toggles cannot replay a completed step.
No provider or customer destination is contacted by the scheduling transaction.

The captured backup team becomes the incident's notification route for later
reminders and attention kinds, with current membership checks and owner fallback.
Issue assignment and claims stay unchanged. Policy changes affect pending and new
incidents; an already escalated incident retains its captured backup team. Recovery
uses the existing latest-notified-recipient rules. Acknowledgment, confirmed recovery,
opt-out, stale parent notification settings, deletion and lost owner eligibility
stop new escalation. Already queued webhook deliveries retain existing controls.

Use the existing `issue.tracker_sync.attention` event with kind `escalation`, a
stable event ID and triage/health links. Reuse the existing bounded inbox and
per-recipient access rules for a separate escalation history. Show current
notification responsibility for an active escalated incident, resolving the live
team or owner fallback. Do not create human handling actions or bump triage
revisions for automated routing. Retained history is evidence of enqueue, not proof
of provider health or successful HTTP delivery. Webhook transport remains at least
once; consumers deduplicate by event ID.

## Consequences

One optional backup team completes the first escalation step while preserving the
existing notification and reminder controls. There is no rotation, repeated chain,
email/chat registration or exact wall-clock delivery promise. New incidents get a
fresh step; prior incidents retain scoped history while their notices are retained.
