# ADR-1493: Reminders for unacknowledged native application issues

Date: 2026-10-16

Status: Accepted

## Context

Native issues now have Slack alerts (ADR-1491) and shared acknowledgment/snoozing
(ADR-1492). Tracker reminders follow provider-sync incidents; application issues
need their own response timing and delivery eligibility.

## Decision

Add an independent owner-only reminder policy on the existing notification settings
page. Default to disabled, a 60-minute interval and three attempts. Owners choose
5–1,440 minutes and one to three attempts per response. Use existing CSRF/session
MFA/deployment-write scope and ownership checks. Revision comparison rejects stale
saves. Identical saves preserve revision, timing and queued work. Changes cancel
outstanding reminder jobs, fence stale completion and allow a full interval before
new work. They preserve acknowledgment, snooze and the response's used attempt count.

Start timing only when a newly selected native application alert is admitted to the
Slack queue while reminders are enabled. Opt-in does not replay historical alerts.
An existing timed response may resume after re-enabling reminder policy, following
a full interval. Changing native Slack policy clears timing; only a newly admitted
alert starts it again. Reopen/regression/split response resets clear timing and
attempt counts along with existing response state.

Schedule at database time after the latest of response timing start, last attempt,
reminder-policy save and snooze expiry, plus the configured interval. Require an
open canonical issue, enabled native Slack/reminder policies, owner entitlement,
active account without abuse hold and an unacknowledged response. Snoozes pause
work and allow a full interval after expiry. Clearing snooze may make an overdue
response eligible immediately. Repeated captures do not postpone the first reminder.

The APID worker processes bounded batches once per maintenance interval. Select and
lock an app with SKIP LOCKED, then recheck due work under the same app lock used by
capture, triage, settings and grouping. Route to the current assigned team's enabled
destination with eligible members, or the explicit native Slack fallback. Record
one atomic attempt and its shared-queue job, or a visible unrouted/queue-full outcome.
Every outcome consumes a slot and advances timing, preventing unavailable routes
from starving other responses or spinning within a batch. Uncertain transaction
failure consumes nothing. Retries of one delivery do not consume another reminder
slot. Reminder attempts are capped at three per response and history at 100 per issue.

Reminder records are a third mutually exclusive shared Slack delivery source,
independent of generic issue activity/webhook events. Queue/history caps, cleanup,
leases, destination revision, captured recipient/assignment checks, retries and
issue thread identity are shared with native alerts. Only fixed labels, IDs,
environment, sequence and Gregale links enter messages. No titles or event evidence
is included. Before HTTP, additionally require current reminder-policy revision,
response identity, open state, no acknowledgment and no active snooze. Manual retry
cannot revive an obsolete response or reminder policy. Acknowledgment cancels only
reminder jobs; snoozing cancels every native job for the issue. Policy opt-out and
membership/routing loss prevent sending. An already sending request cannot be recalled.

## Consequences

Response pages show reminder attempts across responses, selected team and retained
transport status. Queued is admission evidence, not delivery or investigation.
Missing transport history is explicit. Pruning reminder history also removes linked
old delivery records. Existing tracker behavior is unchanged. No generic webhook
kind, public API/SDK endpoint, escalation policy, incident inbox, Slack OAuth or
interactive Slack callback is introduced. Backup-team escalation remains next.

## Validation

PostgreSQL timing, concurrency, opt-in, policy fencing, lifecycle, missed admission,
rollback and shared-thread fake-Slack acceptance; dashboard owner controls and
policy parsing; worker batch/cancellation tests; existing native response and Slack
regressions; SQLC, documentation and spec checks. No VM lifecycle change.
