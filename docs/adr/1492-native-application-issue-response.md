# ADR-1492: Native application issue acknowledgment and snoozing

Date: 2026-10-16

Status: Accepted

## Context

Native application Slack alerts (ADR-1491) have no shared response controls.
Tracker triage belongs to tracker-sync incidents, whose recovery and policy
lifecycle differ from an application issue's resolution and regression lifecycle.

## Decision

Store native response state separately, keyed by canonical issue, with an incident
UUID and monotonically increasing revision. Acknowledgment records actor and
database time; it neither resolves nor assigns the issue and does not silence
future threshold alerts. Timed snoozing pauses native application Slack delivery
for at most 30 days; reason text is limited to 1,024 UTF-8 bytes. Store the most
recent 100 handling actions per issue across responses. Actor references are
historical evidence, never authorization. Account deletion may remove identity
while preserving acknowledgment time.

Authorize the app owner, or a current organization member who is the assigned
account, a member of the assigned team, or a member of the currently eligible
native Slack routing team. Routing uses the same preferred destination/fallback
selection as ADR-1491. Check owner entitlement/status/abuse hold and live membership
on every read and write. The dedicated response page exposes IDs, environment,
assignment and handling history; only the app owner gets the evidence link. This
narrow access does not grant issue evidence, settings, or general app access.

Use session CSRF, MFA and deployment-write scope on dashboard writes. Compare the
expected revision under the existing app lock shared with ingestion and grouping.
Reads use a repeatable-read snapshot. Effective repeated actions add no audit
entry. Preserve response state on reassignment and on the destination of a merge;
merged aliases are inaccessible. Reopen, automatic regression and split-out of an
original group reset response state, rotate incident identity and advance revision,
even if no prior triage row exists. This fences forms opened before a lifecycle
change. Resolution/ignore disable actions; historical handling remains visible.

Snooze atomically cancels pending/failed/in-flight application Slack jobs for the
issue, releases only matching destination leases and fences stale completion.
Check snoozing before admission and again in the shared delivery eligibility view.
An HTTP send already in progress cannot be recalled. Capture, alert evaluation,
activity and generic webhooks continue. Expiry is computed using database time
without a worker; future selected alerts may send, with no catch-up or replay.
Clearing the snooze does not clear acknowledgment. Acknowledgment itself does not
suppress new-issue/regression/frequency/impact notifications. Tracker alerts and
triage are unaffected. Add a response link to native application Slack messages.

## Consequences

Response state can support later reminder/escalation policies without using tracker
incidents as a proxy. This change does not add such policies, a separate native
incident inbox/filter, interactive Slack buttons, OAuth installation, or a public
API/SDK surface. An expired snooze is visible on the response page and enables
future alerts; it does not itself queue a notification. App data deletion cascades
to handling history. Migration rollback is forward-only to preserve response evidence.

## Validation

PostgreSQL lifecycle/routing/authorization/concurrency acceptance, queued and claimed
Slack cancellation tests, owner dashboard CSRF/stale-form acceptance, template
escaping, parser and message-link tests; SQLC generation, documentation and spec
checks. No VM lifecycle changes or live Slack sends are required.
