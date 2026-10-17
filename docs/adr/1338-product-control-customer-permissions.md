# ADR-1338 · Product Control customer permissions

- **Status:** accepted for internal implementation
- **Date:** 2026-10-16
- **Decision:** Map live Application Auth workspace roles to three Product
  Control permissions. Publish the policy as part of the immutable product
  catalog; use its latest revision for customer authorization while commercial
  assignments retain their pinned revisions. Keep the default-off feature flag.

[ADR-1339](1339-product-control-self-service-plans.md) adds a fourth, explicitly
granted Manage plan permission. The original three-permission defaults below
remain unchanged. [ADR-1340](1340-product-control-customer-teams.md) adds the fifth,
explicit Manage team permission and reviewed Auth-backed customer team actions.

## Roles and authority

The permission names are `work:read` (activity, status, events, submission
recovery, results and private files), `work:run` (start and cancel workspace
work), and `billing:read` (usage, statements and credit notes). Run work includes
viewing work, so an operator can follow and recover accepted requests.

`access.roles` maps exact Auth role names to arrays of these permissions. An
explicit empty map denies every customer role; an empty array denies that role.
Unlisted roles receive no access. An explicit policy replaces the whole map;
there is no implicit owner/admin bypass. Product policy never grants platform
owner privileges, changes Auth membership, or supplies Auth permissions.

When `access` is omitted, owners and admins receive all three permissions and
members receive only `work:read`. This deliberately narrows the earlier internal
all-members behavior in ADR-1333/1337, including for existing catalogs that omit
the field. Operators must review their roles before enabling an existing internal
deployment. Unknown custom roles require an explicit product grant. Omitting the
field does not rewrite old catalog hashes. The default mapping is shown in the
owner dashboard and API/CLI inspection.

Application Auth remains the identity and membership authority. Each customer
request obtains the current session's organizations and roles from the latest
catalog's account-owned Auth app at its canonical platform origin. Caller
headers, cached role claims and owner onboarding sessions do not grant product
permissions. Missing/invalid roles, duplicate organization identities, failed
Auth checks and unavailable policy reads fail closed. Membership/session
revocations and policy changes are observed on subsequent requests. A request
already authorized can finish across a concurrent change, as with the existing
membership boundary; streaming authorization is not introduced.

The independent platform-tenant service API retains its explicit platform scope
model. It is not an Auth user session. Product customer cookies cannot be used
as those service credentials or as platform-owner credentials.

## API and portal projections

Every customer route explicitly selects its required permission. The product
bootstrap requires at least one permission and returns only the permitted view.
Billing-only users receive no operations, result data, input schemas or start
quotes. Users without billing permission receive no usage aggregate or billing
documents. Operators still receive their feature prices and remaining start
allowances to confirm paid work. The full role policy is owner-only; the portal
receives only its live role, effective flags and policy revision. Cross-customer
and cross-product operation checks still apply after the permission check.

The portal hides unavailable sections/actions and skips work recovery for
billing-only sessions. A detected role/permission change closes the workspace,
clears rendered data and inputs, and fences pending responses/download bytes;
the user signs in again with current access. Non-secret durable request recovery
metadata remains. Server checks enforce authority even before the next UI poll.

## Reviewed publication

Owners configure the app-wide role map through the existing authenticated,
MFA-protected dashboard or scoped private API/CLI. Cookie API writes require the
exact HTTPS origin; dashboard mutations additionally use signed review and
confirmation CSRF tokens. Tenant principals cannot use the access-policy API.
The owner dashboard edits one role at a time and shows the complete before/after
effective permission matrix before confirmation. Auth roles must be configured
separately; these controls do not create roles or send invitations.

Preview is read-only. Confirmation includes the reviewed catalog head, complete
role policy and expected resulting revision. The server reconstructs the desired
catalog from that immutable base and uses the existing atomic catalog publisher.
It preserves every binding, plan, price and Auth realm in that base. A concurrent
publication conflicts; a lost-response retry succeeds while the desired head is
still current. An old retry after a later publication cannot restore revoked
access. Access publications emit an audit event with before/after revisions.
No new database table or migration is needed.

The CLI saves an exclusive mode-0600 review bound to the product app. Confirmation
validates its entire before/after view against the immutable base catalog and
uses the original request for retries. It accepts no replacement policy selector
during apply. Whole-catalog publication remains an owner configuration interface
and may also set `access`; removing it deliberately selects the default policy.

## Bounds and evidence

Limits live in `pkg/api/limits.go`: at most 32 role entries, 32-byte Auth role
names matching `[a-z][a-z0-9_-]*`, and three distinct supported permissions per
role. Existing 64-KiB catalog/API, 128-KiB review-file/form and Auth response,
token, organization-count and timeout bounds apply.

Tests cover restrictive defaults, unknown and malformed roles, explicit deny,
least-privilege projections, live role/policy changes, revocation/outage failures,
paid admission denial without quota consumption, result/cancel/billing routes,
older customer price revisions, stale publication/retry behavior, immutable
Memory/PostgreSQL policy storage, owner CSRF/origin checks, private CLI reviews,
and browser permission changes with responses in flight. These portable checks
do not replace the qualified live pilot.
