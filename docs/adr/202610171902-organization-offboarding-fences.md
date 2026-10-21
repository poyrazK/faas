# ADR-202610171902 · Organization offboarding and mutation fences

- **Status:** accepted
- **Date:** 2026-10-16
- **Amends:** [ADR-202610171901](202610171901-organization-application-authorization.md)

## Decision

Removing a company member revokes that membership's control-plane authority.
It does not suspend their personal account, revoke their personal sessions or
other organizations' credentials, or stop the company's deployed workloads.

Each membership has an internal `access_revision`. A database sequence supplies
revisions on creation and on role, removal or join-time changes. Restoring a
membership never makes an earlier request valid again. The revision and current
role are loaded together before checking the operation's permission.

Shared application requests and routes using the organization middleware carry
an immutable membership/credential fence in their context. `PgStore` checks it
before each write transaction and holds a membership `FOR SHARE` lock through
commit. Removal/demotion obtains an incompatible row lock. Therefore an earlier
authorized transaction commits before removal, or removal wins and the old
transaction cannot write. No HTTP request holds a database transaction while
waiting for a body, build upload, debugger or network service.

The guarded pool exposes explicit query/transaction methods; it does not promote
unreviewed pool write methods. Existing daemon operations have no request fence
and keep their established persistence behavior. Read-only database transactions
check the membership revision without row locks; they cannot commit customer
intent. The narrow direct `LISTEN` connection remains a session operation.

`db.Notify` and durable notification insertion honor the same transaction check.
A wake or other control-plane notification cannot bypass the fence by writing
directly to the pool. Post-commit organization activity delivery and response
hydration after a self ownership transfer explicitly drop the consumed fence;
they do not create new customer intent.

## Credentials and connections

The member-removal transaction also revokes their active and grace organization
API keys, their deploy tokens for that organization's apps, and unconsumed
invitations to their email in that organization. Credential mint/rotation takes a
membership lock as well, preventing issuance from racing a completed removal.
The migration also revokes credentials belonging to previously removed members.
Rollback rolls back both membership and credential revocation.

Account-scoped, short-lived CI OIDC bearers retain their account identity and
expiry. Their company operations still require the current membership revision;
the fence checks the OIDC token table rather than mistaking them for API keys.

Requests recheck the fence once per second against the store, with a two-second
lookup timeout. Failure cancels the request, including external archive fetches
and upstream log RPCs. This works across API and gateway processes without a
process-local membership cache or dependence on notification delivery. Cancellation
also releases slow HTTP reads/writes. App debugger and app-task WebSocket tunnels
apply the same company boundary and propagate revocation through their upgraded
connection lifecycle. A role change or credential expiry/revocation invalidates
an established connection too. Polling gives bounded eventual connection closure,
not synchronous closure at the instant the removal transaction commits.

## Scope and responses

The reviewed shared app/deployment/build operations, organization management,
live/archive logs and existing creator-only debugger/task tunnels use the fence.
Domain, cron, edge-rule and invocation ID routes resolve their application before
authorization; their permissions remain creator-only with current owner/admin
membership until separately reviewed for sharing.

App, deployment, build, domain, cron, instance, secret, invocation and app-metrics
inventories omit company resources the caller can no longer read. Filtered
pagination walks hidden pages internally and exposes only a visible row's cursor,
preserving access to older personal resources.

Revoked org credentials return 401. A still-valid personal session or credential
cannot resolve a company app after removal (404). A mutation whose grant changed
while in flight returns 403. Previously committed intent, accepted jobs and
runtime end-user traffic continue normally; a multi-transaction HTTP operation
does not retroactively roll back transactions committed before removal.

This does not migrate account-owned jobs, project/environment administration,
other account-wide data services or accounting into organizations. Those surfaces
need their own ownership/permission migration. Organization billing, SSO/SCIM and
service principals remain separate work.

## Acceptance

PostgreSQL tests cover atomic credential revocation/rollback, the two transaction
orders, stale requests after membership restoration, and notification fencing.
HTTP tests pause configuration/deploy bodies after authorization, remove the
creator through the API, then resume and assert no mutation committed while a
remaining owner can still change the app. Gateway race tests establish real HTTP
streams, remove/demote/revoke the caller and assert both client and upstream
connections terminate. Inventory tests include hidden pages ahead of personal
resources and lookup failure closes streams.

This is a local implementation and migration; no production deployment or native
runtime acceptance is implied.

Apply the migration before starting the new binaries. All API and gateway writers
and their old control-plane connections must be upgraded/drained before relying
on the offboarding guarantee; older binaries do not carry request fences.
