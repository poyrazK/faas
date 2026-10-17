# ADR-1491: Native Slack delivery for application issue alerts

Date: 2026-10-15

Status: Accepted

## Context

Team bot destinations, durable delivery, retry/status controls and threaded tracker
incident notices already exist. Application issues still need a customer-operated
webhook bridge. Operators need the same native destination for new issues,
regressions and customer-impact/event-frequency threshold alerts.

## Decision

Reuse team Slack destinations and the existing worker, destination lease, retry
ladder, 24-hour delivery deadline, sealed credentials and delivery controls. Add
an independent owner-only application policy to the existing notification settings
page: opt-in enable flag, selected alert kinds and an optional fallback team.
Use existing named CSRF, session MFA, deployment-write scope and owner checks.
Revision comparison rejects stale saves; identical settings preserve revision and
queued work. No Slack OAuth installer or interactive callback is introduced.

Queue selected `created`, `regressed`, `impact_threshold_reached` and
`frequency_threshold_reached` activity atomically with its capture/transition.
A generic app webhook subscription is not required. Capture the source activity,
policy/destination revisions, issue assignment, channel and eligible recipient IDs.
Never replay retained activity on opt-in, and never create frequency/impact alerts
that their respective policies did not emit. Manual reopen/resolve/assignment are
not regression alerts. Tracker policy opt-in remains independent.

Route first to the assigned team's enabled destination with eligible members;
otherwise use the explicitly selected fallback team. Account-owned/unassigned issues
use that fallback too. Missing routes do not broadcast: increment the owner's
cumulative unrouted counter and retain the ordinary activity/webhook behavior.
Membership alone still grants no app access. A channel's own Slack membership is
managed by the workspace owner; message links require Gregale authorization.

Store application sources as an alternate, mutually exclusive activity FK on the
shared delivery table, with source eligibility in a common SQL view. Application
threads use the captured canonical issue UUID as their thread identity per team
destination revision. Tracker incidents retain their incident identity. Messages
contain only fixed labels, IDs, environment, allowlisted numeric impact/frequency
metadata and local issue links; exclude titles, stack frames, request context and
arbitrary source details. Reuse stable `client_msg_id`; delivery remains at least
once and ambiguous transport timeouts can duplicate a post.

Recheck owner/account/plan/abuse status, destination revision, application policy
revision/selected kind, captured membership intersection, current assignment,
active ignore period and canonical identity before sending. Reassignment, aliasing
by merge and lost eligibility cancel queued application alerts; future real alerts
use their then-current route. Threads and historical messages are not moved by
merge/split. Policy edits cancel outstanding application jobs and fence their
receipts; destination edits fence both sources. An already-started HTTP request
cannot be recalled. Unchanged policy writes preserve claims. Historical failed
jobs for obsolete application policy revisions cannot be manually replayed.

Bound application queue plus delivery history to 1,000 records per app. When full,
remove the oldest terminal record to admit a new alert. If all slots are live,
increment a cumulative skipped counter and continue capture plus app-webhook
commit. Skipped alerts are not replayed. Expire terminal application history after
seven days in a bounded maintenance batch, then clean unreferenced thread roots.
An expired root/history can cause a later alert to start a fresh Slack thread.
Bounds live in `pkg/api/limits.go`. Source deletion, team/destination deletion and
app deletion retain their existing FK cleanup behavior. Source activity is governed
by existing issue history retention, independently of Slack delivery history.

## Consequences

No network call, scheduler or separate delivery service is added to ingestion.
Only enabled selected sources perform routing/admission work. The existing worker
serializes both sources through a destination lease and rate-limit cooldown, while
other teams can progress independently. Queue-full counters distinguish native
chat omissions from generic webhook delivery. Unexpected database failure still
rolls back the source transaction for a clean retry. Richer per-rule channel routing,
Slack acknowledgment actions, workspace OAuth and application incident escalation
remain separate increments.
