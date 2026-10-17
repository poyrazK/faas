# ADR-1485: Tracker notification acknowledgment and claims

Date: 2026-10-15

Status: Accepted

## Context

ADR-1484 routes tracker sync incidents to their owners, but a shared inbox does not
show whether anybody has seen a notice or started investigating. Triage needs to
coordinate people without changing issue resolution or provider delegation.

## Decision

Add local handling state keyed by notification incident identity, shared by every
notice kind in that incident. Acknowledge records the first actor. Claim assigns
the incident to the current actor; claiming does not imply acknowledgment. Only
the claimant or app owner may release a claim. Nobody, including the owner, can
silently steal a claim; release it before claiming it again.

Use the retained notification as the permission boundary. The app owner or a
captured recipient with current org/team membership may read handling state and
history. The same readers may act while the incident is current, its notification
policy remains enabled and its settings revision is unchanged. Membership alone,
a different retained incident on the same link, or a different app grants no
handling authority. Gate dashboard access on the app owner's Issues entitlement;
recipient plans do not need their own entitlement. POST requires session auth,
CSRF, MFA where enabled and the existing deploy-write surface scope.

Serialize writes with worker completion and settings changes using the existing
app lock. Validate the exact notice, live incident, actor and expected triage
revision in the transaction. Reject stale revisions and inactive incidents with
409. A current-revision repeat of an already satisfied action is a no-op; it does
not change the first acknowledgment or add history. Store each real action and
its new revision atomically. Concurrent claims cannot overwrite one another.

Show acknowledgment and claimant names in the inbox, with a dedicated handling
page for actions and history. Retain the latest 20 handling actions per incident,
using a central limit. Preserve actor IDs when accounts disappear; names are
current account labels when available. Claims are historical handling evidence,
not a membership or permission grant. A remaining eligible recipient can ask the
app owner to release a former member's claim.

Recovery and notification settings changes leave handling history intact but
make old incidents read-only. A later incident starts unacknowledged and unclaimed.
Pruning the last retained notice for an incident also removes its handling state
and history; link deletion cascades them. No provider request, issue lifecycle
change, sync baseline update, notification suppression, new webhook event or
reminder scheduler is introduced by acknowledgment or claims.

## Consequences

Teams can see whether an incident is being handled without mistaking a claim for
resolution or recovery. Revision conflicts require a reload. History is bounded
coordination evidence, not an unlimited audit log. Snoozing, reminders and inbox
filters remain separate increments.
