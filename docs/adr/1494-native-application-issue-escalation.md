# ADR-1494: Backup-team escalation for native application issues

Date: 2026-10-16

Status: Accepted

## Context

Native response acknowledgment/snoozing (ADR-1492) and bounded reminders
(ADR-1493) need a backup route when the original responders do not acknowledge.
Tracker escalation belongs to provider-sync incidents and remains independent.

## Decision

Add an independent owner-only escalation policy on the existing notification
settings page. Default to disabled and a one-day delay; allow 15 minutes to seven
days and a backup team belonging to the app. Use existing session CSRF, MFA,
deployment-write scope and owner authorization. Reject stale revisions; identical
saves preserve timing, revision and transport. Changed settings cancel only queued
escalation jobs and fence stale completion. They allow a full delay before any
unattempted response can escalate, preserving prior attempts and admitted handoffs.

Extend native reminder records with a reminder/escalation kind, escalation-policy
revision, responsibility-team snapshot and bounded target-name snapshot. An
escalation has sequence zero and a unique per-response kind/sequence key. Reminder
sequences remain one through three. Reminder and escalation history together retain
100 attempts per issue, with existing shared Slack queue/history limits. No new
public API, SDK, generic issue activity or webhook event is introduced.

Start escalation timing only after a reminder is admitted to the queue; skipped
reminders do not qualify. Backfill the first queued reminder of current responses
on migration, but opt-in still allows a full configured delay from the policy save.
Use the latest first-reminder time, reminder-policy save, escalation-policy save
and snooze expiry plus delay, at database time. Require enabled native Slack,
reminder and escalation policies, an open canonical issue, eligible active owner,
no abuse hold, no acknowledgment and no previous escalation attempt. Snooze expiry
permits a full delay; clearing snooze may make an overdue response eligible now.
Reopen/regression/split clear all escalation and first-reminder state. Changing
native Slack policy clears the first-reminder clock with the existing timers.

APID runs bounded escalation batches before reminder batches under the same app
lock as capture, triage, settings and grouping. Try only the configured backup
team's enabled Slack destination with eligible current members. Missing routes
and full queues consume the single attempt, increment native missed counters and
record a visible outcome, without broadcast or implicit fallback. A backup already
receiving reminders records already_responsible without another message. SQL
failure rolls back the attempt, history, cancellation, admission and handoff.

On admission, cancel outstanding old response-reminder jobs and release only their
matching leases. Persist backup responsibility and restart the regular reminder
interval; do not alter issue assignment. Future response reminders prefer the
persisted backup, with the existing explicitly configured native fallback if it
becomes unavailable. Ordinary new-issue/regression/threshold alerts continue to
follow assignment. Responsibility survives escalation-policy changes; it resets
with the response. A deleted backup leaves fallback routing and historical name
snapshots. Current backup organization/team members gain only the existing narrow
response page and handling actions; no occurrence evidence or general app access.
Current assignment owners retain their existing response access. Reset or membership
removal revokes the backup grant.

Reuse native issue thread identity per destination revision and the shared sender.
Escalation starts a backup thread if none exists there; later backup reminders
reply there. Transport retries keep the same ID and do not create another attempt.
Before HTTP and manual retry, require matching native/reminder/escalation policies,
current response, captured responsibility, assignment and member eligibility, open
state, no acknowledgment and no snooze. Acknowledgment and snooze cancel escalation
jobs through existing native cancellation. An already sending request cannot be
recalled. Messages contain fixed labels, IDs, environment and authenticated links,
without titles, stack/request evidence or arbitrary source metadata.

## Consequences

The response page shows reminder responsibility separately from issue assignment,
and separate bounded reminder/escalation histories with delivery status. Admission
is not proof of receipt or investigation. Unrouted/queue-full/already-responsible
attempts are terminal admission decisions; settings changes do not replay them.
An escalation-policy opt-out does not undo an already admitted handoff; disabling
reminders stops subsequent response reminders. Interactive Slack actions, workspace
OAuth installation and a dedicated incident inbox remain future work.

## Validation

PostgreSQL once-only concurrency, timer/snooze/opt-in, transaction rollback, primary
cancellation, backup response authorization, policy fences, routing failures,
capacity/owner eligibility and regression reset; fake-Slack backup threads and
stable rate-limit retries; HTTP CSRF/bounds/stale settings; parser/batch/template
race tests and existing native alert/reminder/tracker regressions. SQLC, docs and
spec checks apply. No VM lifecycle change.
