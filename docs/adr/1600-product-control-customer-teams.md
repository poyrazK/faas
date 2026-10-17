# ADR-1600 · Product Control customer teams

- **Status:** accepted for internal implementation
- **Date:** 2026-10-16
- **Decision:** Add reviewed customer team actions to the internal portal, using
  Application Auth as the membership, invitation and audit authority. Require
  an explicit Product Control permission and role delegation. Keep both service
  gates off by default.

## Authority and ownership

This extends ADR-1598 with **Manage team** (`team:manage`), granted to no role by
default, including owner/admin. It implies no work, billing or plan permission.
The latest catalog's `team.delegations` maps a manager role to roles it may
assign, change, remove or revoke invitations for. Both a member's existing role
and proposed role must be delegated. Sources and targets must exist in the
product access map; omitted or empty delegation permits no changes.

Product Control uses the live Auth session, selected verified organization,
current product/customer link, active account and tenant, and current Auth app
slug/UUID. It requires current `team:manage` plus Auth `org:read` and
`members:manage`. Product customer suspension denies new changes while allowing
reads and recovery of recorded receipts. Tenant/account suspension denies the
boundary. Product Control disallows self-role changes and self-removal; another
workspace owner manages those. Auth retains its owner-assignment and last-owner
rules.

Delegation limits Product Control actions. It does not reduce an Auth role's
existing authority through other Auth APIs: `members:manage` remains a broad,
workspace-wide administrative permission. Assign only trusted managers. A role
change can affect every product connected to that organization. Reviews display
both Product Control flags and Auth workspace permissions. This milestone does
not introduce per-product memberships or a second identity store.

## Review, transactions and recovery

The Auth template adds `0010_reviewed_team_actions.sql`. Its append-only journal
records the original actor/role, request fingerprint, sanitized result and time.
New Auth team routes lock the organization and role definitions, verify a
canonical snapshot, call existing membership/invitation functions and insert
their receipt in the same PostgreSQL transaction. Current session and workspace
authority are required on every read and retry. Concurrent identical retries
recover one receipt; changed arguments under the same UUID conflict. A retry
after a later role change cannot restore an older role. Receipt IDs are unique
across organizations and a collision rolls back the whole attempted mutation.

Product previews bind the current catalog head, customer assignment, verified
account/product/tenant/Auth realm/organization, actor, normalized request and
fresh Auth snapshot. Confirmation recomputes these inputs before dispatch; Auth
rechecks its snapshot transactionally. Product policy and Auth membership live
in different databases. There is no cross-database atomic transaction: a request
already authorized may finish across a later Product Control policy/mapping
change. Subsequent requests observe that change. Auth membership and role
definition changes serialize inside Auth's transaction.

Product Control checks for a recorded receipt before re-evaluating later
delegations. Exact original confirmed retries can recover their receipt with
current authority, but never perform that old change again. Correlation context
is non-secret metadata, never authorization. Customer receipts strip that raw
context, internal fingerprints, snapshots and unknown result fields. A matching
review hash is returned only for the current product/tenant/Auth instance.

History is scoped to the workspace's reviewed Auth team actions, including
other connected products. Existing direct Auth administration and invitation
acceptance are not retroactively journaled. Journal records cascade when their
organization is deleted; there is no automatic retention expiry in this release.

## Invitations and portal behavior

Invitations use the existing Auth hashed-token storage, expiry, revocation and
single-use acceptance. Recipients use `/ui/invitation#token=...`, authenticate
with the invited confirmed email, accept, then sign in to the product portal.
Configured Auth email delivery is reused. Without a provider, the acceptance
URL is shown once in the confirming response. Plaintext invitation tokens are
never persisted in the journal, Product Control state or browser recovery data.
If that response is lost, receipt recovery cannot reconstruct its link: revoke
the pending invitation and explicitly create a new one.

The Auth email provider receives the existing stable invitation delivery key.
Database changes and receipts are atomic; external email delivery is not part
of PostgreSQL's commit. A delivered email followed by a failed commit may carry
an unusable link. This release makes no exactly-once email guarantee.

The portal requires visible review and explicit confirmation. Before sending it
saves only `{id, hash}` under account/product/tenant in local storage, protected
by Web Locks. Reload and status checks only GET receipts. An unrecorded action
needs a fresh review/confirmation using the saved UUID. Recovery reports the
actual winning receipt if another confirmation won a race. Logout, workspace
switches, role/permission changes, Auth denial and catalog revision changes
fence pending responses and clear private rendered data and invitation links.

## Owner controls, bounds and rollout

Owners inspect/review/apply delegation via the scoped private API, advanced
`gregale product team-policy` commands, or signed dashboard forms. Publications
preserve unrelated catalog terms and use the existing catalog CAS. CLI reviews
are exclusive mode-0600 files; apply reconstructs the entire before/after review
against its immutable base. Delegation publication changes no memberships.

Product Control retains `FAAS_PRODUCT_CONTROL_ENABLED=1` on apid. Auth team
routes separately require `AUTH_PRODUCT_CONTROL_TEAM_ENABLED=1` on the upgraded
Auth app; disabled routes return 404 before authentication and preflight. Existing
Auth APIs remain available according to their own authority. Disabling Product
Control hides its team actions without deleting memberships or receipts.

Limits are centralized in `pkg/api/limits.go` and Auth's existing config table:
32 product roles/delegations, 500 members and pending invitations, 64 Auth roles
and permissions per role, 320-byte emails, 4-KiB correlation context, 1-MiB Auth
responses, 100-action history pages, and invitations from 5 minutes to 14 days
(7 days by default). Existing catalog/body/review/timeout limits still apply.

Evidence includes PostgreSQL migration, contention, retry, revocation, last-owner
and invitation tests; HTTP scoping/authority tests; CLI review checks; and browser
confirmation, recovery and response-fencing tests. A qualified live Auth/email/
exporter pilot remains the release gate; portable fixtures do not qualify it.

See the [runbook](../ops/product-control.md#customer-team-management).
