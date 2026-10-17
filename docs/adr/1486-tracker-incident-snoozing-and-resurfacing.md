# ADR-1486: Tracker incident snoozing and automatic resurfacing

Date: 2026-10-15

Status: Accepted

## Context

ADR-1485 coordinates incident handling, but teams also need to defer a known
tracker sync problem until a chosen time without losing the notice or claiming
that sync has recovered.

## Decision

Add a shared, persisted snooze to the existing incident triage record: UTC expiry,
actor identity and optional plain-text reason. Limit each snooze to seven days and
the trimmed reason to 512 UTF-8 bytes, using central limits. Reject past deadlines,
invalid UTF-8 and NUL characters. The owner or an eligible captured recipient may
snooze, update the snooze or unsnooze an active incident. Apply the same session,
CSRF, MFA, app-owner entitlement, exact-notice membership checks and optimistic
revision rules as acknowledgment and claims.

Save each manual snooze/unsnooze and its history atomically under the app lock.
History records the snooze deadline and reason for that action; later claim or
acknowledgment changes preserve the current snooze without copying its reason
into unrelated history entries. An already unsnoozed incident is an idempotent
no-op at the current revision. Repeated identical snoozes from the same actor
are also no-ops. Keep the existing 20-entry history and notification-linked pruning.

The regular inbox excludes currently snoozed incidents. Add Snoozed and All retained
notices views while preserving team and recipient boundaries. Bind pagination
cursors to the chosen view. Use database statement time to derive whether a snooze
is live. At expiry, notices become visible automatically and sort using the later
of notification creation and snooze expiry; show a returned-from-snooze label.
This works on the next read after downtime without an expiry scheduler or writes
from a GET. Live pagination is not a snapshot: refresh the first page to see
incidents that return ahead of a previously issued cursor.

A snooze is effective only while its incident is current and notification settings
remain enabled at that incident's settings revision. Confirmed recovery or a
settings change therefore ends its inbox deferral atomically with the existing
incident/settings update. Preserve historical snooze metadata and action history.
A new incident has a new identity and starts unsnoozed.

Snoozing affects local inbox visibility. It does not change issue resolution,
tracker delegation, claims, acknowledgment, provider requests or app webhook event
delivery. Expiry is derived visibility, not a fabricated action by a human actor;
the handling page shows its deadline and return status. Email/chat reminders and
expiry delivery events remain separate work.

## Consequences

Teams can defer known problems and find them in a dedicated view. Expired incidents
return automatically, with their original notification timestamps still visible.
Current membership is required even when opening a retained snoozed notice directly.
Recovery can reveal earlier attention notices alongside its recovery notice, since
they are retained evidence and no longer deferred. The latest snooze metadata is
not an unlimited audit archive; history and notices remain bounded.
