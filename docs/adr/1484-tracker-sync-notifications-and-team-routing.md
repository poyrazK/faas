# ADR-1484: Opt-in tracker sync notifications and ownership routing

Date: 2026-10-15

Status: Accepted

## Context

The sync review queue and history explain problems after an owner opens them.
Teams also need actionable incident notifications without polling noise or replaying
uncertain provider writes. Existing app webhooks already provide durable fanout,
retry, delivery history and dead-letter handling. Issues teams do not grant app access.

## Decision

Add an owner-configured, app-scoped notification policy, disabled by default.
Escalate after 3 consecutive failed automatic checks by default, configurable from
1 through 20. Keep threshold bounds and the 50 retained inbox notices per link in
`pkg/api/limits.go`. Pauses and held closures notify immediately. Track incident
identity and consecutive failure state independently of bounded check history.
Only completed automatic checks drive this state; inspections, enables, disables
and manual pulls never announce recovery. A matched/applied automatic check with
no pending write closes an announced incident and emits recovery. Repeated unchanged
problems are silent. A new problem kind within the same incident can emit once.
Settings revision changes restart incident tracking at the next automatic check;
there is no retrospective delivery for already paused policies. Opt-out stops new
notices; previously queued external events remain subject to existing webhook
subscription and delivery controls.

Commit the check receipt, incident state, inbox notice and optional webhook outbox
event together under the existing app/policy locks. An outbox or notice failure
rolls back completion and leaves the claim available for retry. Derive event IDs
from incident identity and kind, enforce incident/kind uniqueness, and use the
existing webhook outbox source ID deduplication and fanout ledger. Pruning inbox
notices does not delete webhook delivery evidence. Reference deletion cascades local
incident/inbox state. All delivery retries remain subject to existing webhook
subscription controls; adding an email/chat destination is not implicit.

Resolve each link to its current canonical issue at attention time. Route to the
assigned team's eligible current members, otherwise the assigned eligible account,
with app-owner fallback for an unassigned issue or empty/ineligible routing target.
Eligibility requires app ownership or active org membership. Capture recipient IDs
and team identity with the notice. Recovery uses the latest attention's captured
recipients, intersected with current org/team membership; do not add later joiners.
If none remain eligible, suppress that recovery. Assignment alone does not resend
a paused incident. Existing issue assignment events remain available for handoffs.

Expose a bounded, paginated local notification inbox. The owner may inspect all
app notices; other readers must be captured recipients with active org membership
and, for team notices, current membership in the captured team. Recheck membership
on every read and bind cursors to app, owner, actor and team filter. Owner-only
settings use CSRF, MFA, deploy-write scope and revision comparison. Changing these
settings does not grant or alter lifecycle delegation.

Captured recipients with current org/team membership may read their retained
notifications and the linked credential-free local sync health history. Gate this
narrow access on the app owner’s Issues entitlement, rather than the recipient’s
plan. This extends ADR-1483’s owner-only health reads for those specific links; it
does not grant general app or issue access. Membership alone is insufficient.
Keep fresh provider inspection and all resume/configuration actions owner-only,
with both hidden controls and server authorization.

Add `issue.tracker_sync.attention` and `issue.tracker_sync.recovered` to the closed
webhook vocabularies and generated subscription SDK enums. Payloads contain
incident/kind, app/issue/link/provider identity, stable outcome/reason, bounded
failure count and threshold, routing IDs and local issue/health paths. Never send
credentials, raw provider errors or event payloads. Failure counts saturate at the
maximum configurable threshold. Inbox reads and settings do no provider I/O.

## Consequences

Owners and teams gain a durable incident inbox and optional app webhook delivery,
including explicit recovery after a confirmed automatic check. New subscriptions
do not receive historical outbox events. Notifications do not retry provider writes,
resume policies or resolve issues. A queued webhook is an app-level delivery with
captured routing metadata, not a personal message; receivers choose how to route it.
The inbox retains recent evidence rather than acting as an unlimited audit archive.
GitHub and Linear tests use fake clients and delivery-ledger fanout; live customer
provider/webhook destinations are not exercised.
