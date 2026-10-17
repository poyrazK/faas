# ADR-1482: Tracker closure review queue and deployment evidence

Date: 2026-10-15

Status: Accepted

## Context

Directional tracker sync holds provider closures until an owner selects a fixing
deployment. Finding those holds one issue at a time makes review difficult.
A recent deployment alone does not establish that a problem is fixed.

## Decision

Provide an owner-only, app-scoped dashboard queue of saved tracker observations.
Filter held closures, conflicts, unconfirmed writes, and other blocks or errors.
Use bounded keyset pages ordered by immutable reference creation time and ID;
bind cursors to the app and filter. Resolve each reference to its current canonical
issue group, marking delegation that predates regrouping. A partial app index
bounds the policy scan to enabled items needing attention. Disabled and healthy
policies do not appear. Errors may still be retried by the worker; listing them
never changes retry behavior.

GET requests read local state only. Queue forms request the existing authenticated,
CSRF-protected provider preview. Resolution still requires a fresh scoped provider
read, explicit deployment selection, and the existing transactional intent,
policy, lease, connection and environment checks. There is no bulk resolution or
mutation directly from the queue.

Enrich the existing bounded recent-deployment selector with deployment status,
commit identity, the current group's latest occurrence time, and summed recorded
occurrences for each deployment across all original group members. Preserve the
same app/environment scope as the final resolution action. Rank live deployments
created after the latest occurrence with zero recorded occurrences as suggested
candidates within that recent list. Never preselect a candidate. Creation time is
not rollout time, and missing occurrences can reflect sampling or retention;
neither is evidence sufficient to authorize resolution. Explain this beside the
selector and show the evidence for each choice. A new occurrence can change this
advisory ranking; the owner still asserts the selected deployment fixes the issue.

## Consequences

Owners can discover held closures and reach the guarded confirmation flow from
one page. Existing APIs, worker dispatch, manual resolution safeguards and SDKs
remain unchanged. Suggestions are advisory and require owner confirmation.
The queue is a current-state view, not a snapshot or a complete failure history;
items may change or disappear between pages and provider access happens only
when the owner explicitly requests a fresh review.
