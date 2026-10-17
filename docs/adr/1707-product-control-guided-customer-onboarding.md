# ADR-1707 · Product Control guided customer onboarding

- **Status:** accepted for internal implementation
- **Date:** 2026-10-16
- **Decision:** Connect live Application Auth workspaces to reviewed customer
  setup through the private API, CLI and owner dashboard. Commit customer
  creation, assignment and an immutable setup receipt together. Keep the
  existing default-off Product Control flag and native qualification boundary.

## Identity and review

The owner must authenticate through the existing platform session/API-key,
scope, MFA and verified-email policies. Cookie-authenticated API mutations
additionally require the exact HTTPS origin; dashboard forms require both that
origin and a signed CSRF confirmation.

A separate, short-lived Application Auth user session selects workspaces by name
or slug. The upstream is resolved from
the latest catalog's account-owned Auth app, using its canonical platform host.
The Auth app verifies the live session and returns organization memberships.
Only explicit `owner` and `admin` roles may link a workspace. Both preview and
initial confirmation recheck membership; JWT claims alone grant no authority.
All members of a linked workspace retain the portal access already defined by
ADR-1703. Review makes that access explicit before confirmation.

API/CLI user access tokens are transient POST body fields. The CLI reads a
bounded private token file instead of accepting token values in arguments and
never includes credentials in reviews, receipts or output. The dashboard uses
password sign-in to the existing Auth app and retains only its short-lived
access token in a Secure, HttpOnly, SameSite=Strict cookie scoped to this
owner/app's onboarding path and Auth-app identity, for at most ten minutes.
The connection has no refresh token. Disconnect clears the local connection;
it does not claim to sign out other Application Auth sessions.

Invitations, workspace creation, recovery and membership administration continue
through Application Auth. Product Control provides Auth and portal entry links
and guidance. It neither collects an Auth admin key nor sends invitations or
creates organizations during setup. These links contain no session credentials.

## Atomic setup and recovery

Choose an existing owned tenant, or provide a new stable external reference and
name. Preview writes nothing. New customers adopt the reviewed catalog revision.
An existing customer's pinned revision is the default; its plan, suspension and
established identity cannot be changed by onboarding. A first organization link
may be added when its plan/revision is unchanged. Use the existing reviewed
plan-change workflow for other changes. A workspace cannot be shared by two
customers in the same product.

Confirmation rechecks the catalog head, realm identity, UTC month, customer
state and tenant quota under the account → shared app → tenant lock order.
The preview hash binds all reviewed terms and identities. Customer creation,
plan/link assignment and `product_control_onboardings` insertion share one
transaction. Conflicts or quota exhaustion roll back the entire action. The
Memory implementation follows the same contract under its store lock.

The caller retains a UUID and canonical request/hash. An exact retry returns its
immutable receipt before requiring a new Auth session, even after a later plan
change. Returning that receipt performs no assignment mutation. Changed arguments
with the same ID conflict. Competing initial setups require a fresh review after
one commits. API reads and dashboard receipt links resume after a lost response;
the CLI saves an exclusive, mode-0600 review file and confirms it explicitly.

## Activation evidence

Status separates retained setup, current assignment equality, configured workspace
mapping, historical customer access and verified success. Owner setup sign-in
does not mark customer access. A subsequent authenticated customer product-view
request, after live Auth membership verification, records its first access time
only while the current realm, mapping and active tenant/customer still match.
The timestamp is a historical observation, not proof of present membership.

First success comes from independently retained commercial receipts whose
admission followed this setup and whose outcome was verified successful. It is
not inferred from an invitation, accepted request, missing result or charge.
Expired operation results do not erase that proof. PostgreSQL status reads use
a read-only repeatable-read snapshot; Memory returns independent copies.
Changed/suspended identity configuration hides a prior access timestamp from
the current-access projection. Historical setup and success remain identifiable.

## Bounds and validation

Limits live in `pkg/api/limits.go`: 32-KiB API bodies, 128-KiB review files,
ten-minute connection cookies, 128-byte customer/workspace names and 256-byte
external references. Existing Auth response, organization-count, token and
timeout limits apply. Customer suggestions use existing bounded pagination.

Memory/PostgreSQL contracts cover atomicity, quota boundaries, stale catalog and
realm changes, identity uniqueness, concurrent confirmations, pinned terms,
retained retries and activation evidence. HTTP tests exercise live-role changes,
owner/customer separation, CSRF/origin checks, scoped cookies, reviewed prices
and flag closure. CLI tests verify file permissions, no implicit confirmation,
stable retries, unambiguous workspace selection and credential-free reviews.
Portable fixtures do not qualify native execution, live Auth deployments,
email delivery or private storage. The separate live pilot remains required.
