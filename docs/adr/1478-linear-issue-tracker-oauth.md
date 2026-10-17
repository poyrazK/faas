# ADR-1478: Linear Issue tracker OAuth

Date: 2026-10-15

Status: Accepted

## Context

Manual personal API keys make provider onboarding and credential lifecycle hard to
operate. Linear's current OAuth protocol uses PKCE, 24-hour access tokens and
rotating refresh tokens with a 30-minute replay grace for uncertain refreshes.
OAuth must preserve the existing owner, team, reference and policy safeguards.

## Decision

Operators configure `FAAS_LINEAR_OAUTH_CLIENT_ID`, `FAAS_LINEAR_OAUTH_CLIENT_SECRET`
and `FAAS_LINEAR_OAUTH_REDIRECT_URI` at boot. All unset disables the button; partial
or invalid configuration fails boot. Redirect URI must be an absolute HTTPS URL
ending in `/oauth/linear/callback`, without query, credentials or fragment. Token
and consent destinations are fixed Linear origins; HTTP redirects are not followed.
Request user-actor `read,write` consent with S256 PKCE and `prompt=consent`, allowing
the user to select a workspace in Linear. No admin scopes are requested.

Owner session, MFA, deploy-write permission, Issues entitlement and named CSRF gate
the start/finish forms. The callback also requires the owner session and MFA. Bind
256-bit random state to a Secure HttpOnly SameSite=Lax browser cookie, the owner,
app, originating issue, configuration revision, OAuth client ID and redirect URI.
Store only a state hash and an age-sealed PKCE/token envelope. Authorization state
has a 10-minute lifetime and transitions once from authorizing to exchanging,
choosing and consuming. Consume before a remote code exchange; unknown exchanges
require new consent rather than replaying the code. A clean redirect removes the
code before rendering the picker. Callback responses use no-store/no-referrer.

After exchange, read a complete bounded catalog of active accessible teams and
verified workspace/viewer identity. Show the selected workspace and named teams.
Confirm the selected team again at the provider and CAS the current connection
revision before storing credentials. A concurrent owner connection change wins.
Store access/refresh tokens in a distinct scoped age envelope; no token is sent to
the browser. Replacement increments the connection revision and requires existing
automatic policies to be reviewed/re-enabled. Optional callback signing secrets
remain independently scoped; omission on replacement disables callbacks.

Preserve personal API key support. OAuth supplies an internal Bearer authorization
value; API key input still rejects whitespace and uses the existing raw key header.
Public metadata and all SDKs expose only auth_method, oauth_status and access token
expiry. The HTML callback/picker follows the existing session OAuth exclusion from
Bearer DTO/SDK route parity. Consent is a dashboard flow, not a public token API.

Refresh within one minute of access expiry under a durable 30-second lease, fenced
by app owner, connection revision, credential generation and claim identity. Keep
the prior refresh token until the new pair is durably sealed. Normal rotations
increment only credential generation, retaining connection revision and delegated
policy binding. Persist returned rotated tokens before a fresh provider identity
check so a temporary verification failure cannot discard the only new refresh
credential. Every native operation still rechecks workspace, viewer and team before
using a token for ticket writes.

Unknown refresh outcomes release the lease but retain the first-attempt timestamp
and old credential, permitting replay only within Linear's 30-minute grace. Expired
recovery or an explicit invalid_grant requires reconnect; revoked read credentials
also set reconnect_required. Temporary failures remain retryable. Commit refresh
results with a bounded four-second detached context. Stale, replaced, disconnected
or expired claims cannot overwrite a newer credential.

Explicit owner revocation first fences native use with revoking, calls Linear's
refresh-token revocation endpoint, and clears local credentials only after HTTP
success. Uncertain/non-success revocation stays fenced and retryable; users can
remove authorization in Linear and disconnect locally if the endpoint cannot
confirm an already revoked token. Ordinary disconnect erases local credentials;
it does not claim to revoke provider authorization. Remote operations and local
commits are not atomic. Never promise grant revocation from access-token expiry
or an ambiguous provider response.

Keep one bounded flow per app and delete expired flows in maintenance batches of
100. Catalogs scan at most 200 teams in pages of 50. Flow, lease, commit, catalog,
refresh and expiry bounds live in `pkg/api/limits.go`. Disconnect/revocation remain
available when the Issues entitlement or deploy eligibility has lapsed.

## Validation

Provider tests cover forms, PKCE, scopes (including legacy arrays), expiry, Bearer
headers, revocation, pagination and sanitized failures. Credential tests cover
cross-scope rejection, encryption, expiry and independent webhook verification.
PostgreSQL/browser acceptance covers cookie mismatch, account scope, one-use code
and team confirmation, escaping, native Bearer reads, refresh contention, uncertain
rotation/recovery, grace exhaustion, stale completion, invalid grants, uncertain
and confirmed revocation, connection CAS and expired state. Existing native API
key and callback regressions remain included. SDK tests cover metadata without
exposing credentials. All provider traffic in acceptance is mocked; live consent
requires operator registration and credentials.
