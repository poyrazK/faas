# ADR 20261017191505: MCP browser OAuth and guarded renewal recovery

## Decision

Add an explicit `browser_redirect_uri` to guided role configuration, registered
with the OAuth provider. It must be an HTTPS origin plus
`/dashboard/mcp/oauth/callback` with no credentials, port, query, fragment or
encoded path. The dashboard additionally requires an exact match to its
configured public console URL (`CLIAuthURLBase`), never the request Host header.
Expose configuration through `--oauth-browser-redirect-uri` in monitor setup.
CLI authorization continues to accept only its reviewed loopback redirect.

Start authorization through an authenticated, body-bounded, account-bound CSRF
POST carrying the role and revision. Reuse the provider compatibility checks,
PKCE and guarded provider transport. Seal session binding with PKCE and redirect
URI in durable authorization state; retain a private session digest for atomic
observer exclusion. A separate encrypted HttpOnly/Secure/SameSite=Lax five-minute
callback cookie binds the account, live session ID, application, role, revision,
state and expiry. Starting another flow in that browser replaces the cookie.

Verify the live session, cookie, constant-time state match and revision before
consuming authorization. Browser and CLI completion cannot consume each other's
flows. A durable compare-and-swap permits only one code exchange. A denial
consumes and clears its flow without exchanging a code. Lost replies and failed
grant installation retain the existing grant-retirement record; never replay a
consumed exchange. Unknown issuance records must be manually resolved in the
retirement ledger before beginning another flow. Expired unknown-issuance records
require explicit provider-resolution confirmation, because their discarded sealed
material can no longer be manually acknowledged through the retained-grant API.
Uncertain renewal requires
explicit provider-resolution confirmation before reauthorization.

Authenticate callbacks with their query removed from login/MFA return URLs,
then restore the query only inside the authenticated callback. Use no-store and
no-referrer headers. Redirect completion to fixed dashboard outcome messages;
never echo provider descriptions, authorization codes or token values. Active
browser flows exclude catalog observer claims for that role; lease checks in
begin protect against an observer that won the race.

## Renewal recovery

Add `retry_renewal` to the existing OAuth API and a matching dashboard form.
The state store schedules the observer under account ownership and revision
CAS; it does not perform a provider exchange on the POST. Preserve a recorded
successful response for installation and preserve journal identity/history.
Advance the revision so concurrent or repeated stale submissions are refused.
Do not retry pending/unknown issuance, reauthorization-required credentials,
active authorization flows, live leases, paused or disabled observers. A known
rejected renewal or a credential lookup failure can be scheduled after correction.
A queued result is not confirmation of successful renewal.

## Validation

Cover exact redirect validation, PKCE, account/session/role/revision isolation,
CLI/browser separation, denial, expired callbacks, replay and uncertain issuance.
Check observer exclusion and renewal journal/lease fences, including preservation
of saved responses. Verify callback login redirects do not contain codes or state,
and templates omit private authorization material.

Local validation ran the new state recovery and existing renewal-fence tests
against the package's production Go files, including a disposable PostgreSQL
schema. Compiling the unrelated external state test suite exceeded this
workspace's disk capacity. The browser runner tests, existing guided OAuth and
retirement regressions, dashboard rendering, authenticated dashboard routes,
CLI callback configuration and OpenAPI shape checks ran through their regular
package test suites. Provider exchanges use controlled test transports; this
change does not qualify a live provider deployment.
