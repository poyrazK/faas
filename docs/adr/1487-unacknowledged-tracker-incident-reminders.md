# ADR-1487: Bounded reminders for unacknowledged tracker incidents

Date: 2026-10-15

Status: Accepted

## Context

Incident notices, claims and snoozes provide local coordination, but an active
incident can remain unacknowledged indefinitely. Owners need optional reminders
that survive restarts and use existing webhook delivery infrastructure.

## Decision

Add a separate owner-configured reminder policy per app, disabled by default.
Default to a 24-hour interval, configurable from 15 minutes through seven days.
Allow at most three reminders per incident. Keep all limits and the ten-item
sweep budget in `pkg/api/limits.go`. Reminder settings have their own revision and
update timestamp: saving them does not invalidate notification incidents, triage
state or lifecycle delegation. Enabling reminders or changing the interval starts
a full interval. Reconfiguration does not reset an incident's used reminder budget.

Persist incident opening time, reminder count and last enqueue timestamp. Preserve
these fields across automatic checks for the same incident identity, and reset
only when that identity changes. Eligibility requires enabled reminder and
notification policies for the current owner and incident settings revision, a
live app, the owner's current Issues entitlement and deploy eligibility, an
unacknowledged incident and an elapsed interval. Claims alone do not acknowledge
an incident. The due time is one interval after the latest of incident opening,
reminder policy update, prior reminder or snooze expiry. Snoozes therefore pause
reminders and allow a full interval after expiry; manual unsnooze can make an
already overdue incident eligible on the next sweep.

Use a bounded apid sweep on the existing one-minute maintenance cadence. Select
and lock a due app with `SKIP LOCKED`, recheck eligibility, and atomically persist
the reminder notice, captured webhook outbox event, routing receipt and counter.
The app lock serializes this with acknowledgment, snoozes, recovery, assignment
and opt-out changes. A failure rolls back all records. Concurrent workers cannot
overwrite or duplicate a reminder. No provider request or lifecycle action occurs.

Route each reminder to the issue's current eligible assigned team or account,
with app-owner fallback. Update the incident's latest notified routing so a
later recovery uses those captured recipients, intersected with current membership.
Reminders use the existing `issue.tracker_sync.attention` event with kind `reminder`,
a one-based sequence, deterministic incident/sequence ID, and local triage and
health paths. They carry the last reported safe problem reason rather than claiming
a new provider observation. Existing subscription, retry, fanout ledger and delivery
controls apply; transport retries may repeat an HTTP delivery with the same event ID.
No email/chat destination is registered implicitly.

Extend notice uniqueness to incident/kind/sequence, retaining sequence zero for
existing notice kinds and preserving their deterministic IDs. Reminder count is
independent of inbox retention, preventing pruning or worker restarts from replaying
used slots. Show reminder notices in the existing inbox and a scoped reminder
history on the handling page. Each history row requires current recipient/org/team
eligibility or app ownership. Retain the existing bounded inbox and reference
cascade cleanup. Do not invent human handling actions or alter triage revisions
when an automatic reminder is queued.

## Consequences

Acknowledgment and confirmed recovery stop future reminders. Notification opt-out,
reminder opt-out, changed incident settings, deletion or lost owner entitlement
also prevent enqueue. Already queued webhook deliveries remain governed by existing
subscription controls. Scheduling is best-effort after a due time, with a bounded
sweep; it does not promise exact wall-clock delivery. New incidents start with a
fresh budget, while recovered incidents retain eligible reminder history.
