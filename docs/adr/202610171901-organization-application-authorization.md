# ADR-202610171901 · Organization application authorization

- **Status:** accepted
- **Date:** 2026-10-16
- **Amends:** ADR-061's personal-only flat application routes for the reviewed
  resource operations described below. Organization management remains explicitly
  path-scoped; no session-selected active organization is introduced.

## Decision

The persisted application's `org_id` authorizes shared application operations.
Globally unique application slugs and deployment/build IDs identify the resource;
headers and query parameters cannot select a different organization. A current
membership and the operation's explicit role permission are required, including
when the caller originally created the app. Missing resources, foreign
organizations and removed memberships return the same 404 resource response.
Insufficient roles return 403. Lookup failures fail closed.

`pkg/apid/appaccess` supplies a request-bound grant for exactly one app. The
authenticated principal remains the human or API-key creator. Reviewed legacy
handlers receive the app's existing account as their storage, entitlement and
quota subject. No principal is replaced, no account-wide endpoint receives that
subject, and no billing or network-tenancy cutover occurs in this change.

## Permissions and credentials

- Owner/admin/developer/viewer can inspect the reviewed app, configuration
  metadata, deployment/build evidence and live/archived logs. Billing membership
  continues to use organization inventory and billing surfaces.
- Owner/admin/developer can configure, deploy, park/wake and manage secrets.
  Secret plaintext is never added to read responses.
- Security policy, trusted publishers and application deletion/restoration
  require owner/admin.
- Existing API scopes, app-bound deploy tokens, MFA, fresh step-up, verified
  email, same-origin protection and plan checks still apply. Verification and
  actor attribution use the original principal; limits use the resource account.
- Organization keys cannot cross their stored organization binding. Account
  keys cannot gain access to another creator's shared app; use an explicitly
  organization-bound key or an authenticated user session. Unattributed legacy
  apps remain account-owned and cannot be adopted by an organization hint.

Reviewed operations are enumerated in `pkg/apid/appaccess/routes.go`. New routes
do not automatically grant shared access. For an unreviewed operation, only the
original creator with a current owner/admin membership retains legacy access;
the handler never receives another account's authority.

## Replay and attribution

Resource authorization runs before route idempotency. New collaborator receipts
are partitioned by actor and credential within the existing resource account,
method and path. Creator receipts retain their existing namespace so an upgrade
does not repeat a previously accepted deployment. A demoted or removed member
cannot replay a successful mutation to bypass the new permission. Audit metadata
and deployment/activity attribution retain the real actor independently of the
resource account.

## Boundaries and acceptance

This is the first application authorization slice. Account-wide list/aggregate
APIs remain creator-scoped; additional shared data, network, environment and
project operations require separate route reviews. Billing and quota ownership
remain on the existing account. Existing streams are authorized at establishment;
active-stream revocation and in-flight mutation fencing belong to offboarding.

Portable acceptance covers role denial, creator demotion/removal, key scope and
tenant boundaries, indirect deployment access, pre-replay authorization,
configuration/secret storage under the resource owner, real deployment actor
attribution, and gateway live/archive log access. This change does not modify VM
lifecycle or enable a production rollout.

Follow-up [ADR-202610171902](202610171902-organization-offboarding-fences.md) adds active-stream
revocation, mutation fencing and filtered core account inventories.
