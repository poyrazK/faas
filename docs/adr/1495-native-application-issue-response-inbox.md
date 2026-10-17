# ADR-1495: Native application issue response inbox

Date: 2026-10-16

Status: Accepted

## Context

Native acknowledgment, snoozing, reminders and backup escalation have per-issue
response pages. Responders need a bounded list of issues they can currently handle,
without permission to inspect application occurrence evidence.

## Decision

Add a session-authenticated per-app inbox at
`/dashboard/apps/{slug}/issues/responses`. Show only open canonical issues that the
actor can handle now. Extract the existing response authorization into a shared
SQL view used by both the inbox and individual response reads/writes. Owners need
an eligible active account without abuse hold. Other actors must remain current
organization members and be assigned directly, belong to the assigned or persisted
backup team, or belong to the currently eligible native reminder delivery route.
History, old recipients and old backup grants establish no access. No title,
stack, occurrence payload or general app access is added.

Filter before pagination by all open responses, unacknowledged and awake (default),
currently snoozed, unacknowledged resurfaced, escalated, or latest response delivery
failure. These filters can overlap. The team selector narrows authorized rows to
current members of the reminder-responsible or currently routed team; being the
owner or a directly assigned account alone does not satisfy this team filter.
Use the existing 50-row issue page limit with one lookahead and an ascending UUID
cursor. New filters start at the first page. No total count or cross-app search.

Read current response revision, environment, responsibility and eligible route,
plus the latest reminder/escalation admission and retained delivery status for the
current response. The failure filter includes unrouted/queue-full admissions and
failed/dead transport. It excludes historical responses and ordinary occurrence
alerts. Retention can remove delivery detail. Compute nullable reminder/escalation
eligibility times with the same policies, budgets, snooze and response clocks as
the workers, using statement time. These are eligibility estimates, not promises
of admission, delivery or receipt. The inbox itself does not enqueue work.

Allow acknowledge, snooze and clear-snooze from each row. Reuse existing CSRF,
MFA/scope, authenticated actor, app-lock, live authorization and revision checks,
as well as cancellation and handling audit behavior. Forms use the existing
`/dashboard/apps/{slug}/issues/{issue_id}/triage` endpoint with the fixed
`return_to=responses` marker. Validate the filter before mutation, then redirect to a fixed
inbox path with validated filter parameters; never accept a return URL. Return to
the first page so changed filter membership cannot strand the responder on an
empty cursor page. Keep acknowledgment and snooze inputs in separate forms.

## Consequences

Team members can discover and handle authorized responses without Slack delivery
or evidence access. Membership removal, reassignment, grouping, response reset,
owner suspension and abuse holds take effect on subsequent reads/actions. The
list is a live paged snapshot, not an immutable export; changes between pages may
change results. Resolved/ignored issues remain outside this open-response inbox.
No new public API, SDK, webhook, activity kind, queue or policy is introduced.
Interactive Slack actions and workspace OAuth installation remain future work.

## Validation

PostgreSQL pagination, scope parity, team membership, backup reset, response filters,
due clocks, current-response failure retention and abuse holds; HTTP filter
validation, CSRF, snooze/clear/acknowledgment and stale revisions; template escaping,
separate forms, safe links, filter tests and ServeMux route compatibility. Re-run existing native response,
reminder, escalation and Slack regressions, SQLC and spec checks. No VM change.
