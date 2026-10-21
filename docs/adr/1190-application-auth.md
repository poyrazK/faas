# ADR-1190: Application Auth on a pinned GoTrue server

- **Status:** proposed; phases 1–6 implemented (the `auth` credential mode; the template and `gregale auth create|status`; organizations, roles, `authorize()` and `--auth`; email, hosted pages and `@gregale/auth`; storage, realtime and workflow checks; owner administration, the dashboard screens and user import; signing-key rotation and typed settings; lifecycle events and the activity view; passkeys, branded hosted pages, CAPTCHA and activity retention; Go, Python and Node backend SDKs; phone sign-in by SMS; organization SAML single sign-on; SMS and recovery-code second factors with a hosted verification step; SCIM provisioning; self-service organization SSO with DNS domain verification; the OAuth 2.1 authorization server; anonymous users and identity linking), not yet qualified on a live backend or native builder
- **Date:** 2026-10-13
- **Decision:** Package a pinned Supabase Auth (GoTrue) server with a Gregale front process as an ordinary Gregale app bound to the customer's managed PostgreSQL. Add Gregale-owned organizations, invitations, roles and one SQL authorization function that the Data API, object downloads, realtime channels and workflow starts all use.
- **Why:** Gregale authenticates its own accounts, and its gateway, Data API and realtime service verify tokens from an identity provider the customer brings. Gregale issues no tokens for an application's own users, so every customer must still design sign-up, sessions, recovery, membership and permissions alone.

## Context

Account security (MFA, dashboard OAuth with PKCE, organizations under ADR-061,
session revocation under ADR-039) protects people who operate Gregale. It does
not give an application a user directory. The pieces that consume application
identity all start from an external issuer:

- Edge JWT rules (ADR-091) verify a configured JWKS at the gateway.
- The Data API (ADR-650) verifies an external JWT and passes `sub` and the
  claims to PostgreSQL row-level security.
- Realtime v2 (ADR-369) asks an application callback whether a verified OIDC
  principal may read a channel.
- Durable browser sessions (ADR-871) expect the application backend to have
  authenticated its user already.
- Platform tenants (ADR-244) map a JWT claim to a tenant.

ADR-650 established how Gregale ships an upstream server as a product: pin it
in a template, deploy it as an ordinary app through the builder and deploy path,
and give it a schema-restricted managed PostgreSQL credential. That adds no
resource owner, deploy path, metering model or plan quota.

## Decision

### Engine

The engine is `supabase/auth` (GoTrue), MIT licensed, pinned to v2.197.0 by
image digest. The template pins the linux/amd64 manifest
(`sha256:839f5294…`) of the release index rather than the index itself:
Gregale builders are x86_64, and the index's arm64 variant ships a binary that
fails with `exec format error`. It provides email and password, magic links, one-time codes,
OAuth providers, TOTP and passkey MFA, refresh-token rotation, sessions,
sign-out, an admin user API, asymmetric signing keys and a JWKS endpoint.
GoTrue stores everything in a PostgreSQL schema, so users live in the
customer's own database next to the rows RLS protects. A version bump is a
template change that must re-run the migration overlay check below.

### Packaging

[ADR-1711](1711-verified-application-auth-release-migrations.md) supersedes the
upstream migration invocation below: the release runner applies verified files
directly because the pinned GoTrue binary's `migrate` command uses embedded SQL.

`gregale auth create NAME --database DB` reserves an ordinary app, attaches an
`auth` binding, seals the settings as app secrets and deploys the `auth`
template, following `data-api create`. The app has two processes:

- `release: node migrate.mjs` runs `auth migrate`, and later the
  `gregale_auth` migrations, as a release task. Only this session raises the
  statement timeout, to `AuthMigrationStatementTimeoutMS`.
- `web` runs the Gregale front process, which supervises `auth serve` on
  loopback. GoTrue's default command migrates before serving and is never
  used, so a cold boot or snapshot restore performs no DDL.

The front serves `/auth/v1/*` by proxying to GoTrue and
`/auth/v1/.well-known/jwks.json` unchanged. It also serves the hosted pages,
the `/authz/v1/*` organization and policy API, and a loopback-only email hook.

The CLI generates each ES256 signing key locally and stores it only as its own
sealed secret, `AUTH_JWT_KEY_<ID>`; it never prints or logs a key, and a
resumed or repeated create never replaces existing signing material. An
`AUTH_JWT_SIGNER_<ID>` secret holds the Unix time a key was made the signer,
and the newest one names the key that signs. The front passes every key to
GoTrue as `GOTRUE_JWT_KEYS`, with only the signer allowed to sign, and refuses
to start without exactly one newest marker naming a configured key; at most
four keys are kept (`pkg/api/limits.go`).

Secrets are write-only, so a single secret holding all keys could not be
rotated without reading a private key back. With one secret per key, rotation
only adds and removes secrets: `gregale auth keys create` adds a standby key,
which the restarted app publishes in its JWKS and accepts but never signs
with; `keys activate` writes the newer marker and then deletes the old ones,
so a restart at any point finds a signer; `keys revoke` removes a key that no
longer signs. `keys rotate` does the first two and waits for the running app
between them. Gregale's verifiers refetch a JWKS early when a token names an
unknown key, but publishing the standby key first also covers verifiers that
fetched the JWKS from an instance that had not yet restarted. The CLI reads
key state from secret names and from the front's public `/settings/v1`, which
reports the signing and verifying key IDs. It refuses to revoke a key that
signs in the running app or has a marker, so two operators cannot delete the
key that is about to sign and stop the app from starting. A revoked key's
tokens stop verifying when the app restarts, and in the gateway, Data API and
realtime when their JWKS caches expire, within five minutes.

The front starts GoTrue with an explicit environment, never its own. Customers
configure providers, MFA, password rules and CAPTCHA through
`GOTRUE_EXTERNAL_*`, `GOTRUE_MFA_*`, `GOTRUE_PASSWORD_*` and
`GOTRUE_SECURITY_CAPTCHA_*` app secrets; any other `GOTRUE_*` secret stops
startup rather than being ignored. `gregale auth config` and
`gregale auth providers`, and the dashboard's Auth page through apid's session
and CSRF-bound forms, write those secrets from typed settings validated by one
shared package (`pkg/authsettings`): sign-up, email confirmation, site and
redirect URLs, minimum password length and required character classes,
leaked-password checks, TOTP enrolment and nineteen OAuth providers. Turning
MFA off stops new TOTP enrolments only, so users who enrolled keep verifying.
The front sets every enabled provider's callback to its own `/auth/v1/callback`.
`/settings/v1` reports the running configuration without credentials, since the
CLI and dashboard cannot read secrets back. GoTrue rate-limits on `X-Real-IP`, which the
public gateway replaces with the verified client address. GoTrue v2.197.0
logs requests and audit events at info, with user email addresses, even when
its log level is set to `warn`, so the front reads GoTrue's output and drops
info and debug lines, and replaces personal fields (`actor_username`, `email`,
`phone`) in the warnings and errors it keeps. `--autoconfirm` (development)
and `--disable-signup` map to GoTrue's own settings.

### Database credential mode `auth`

GoTrue's migrations enable RLS on every `auth` table, and its runtime needs
DML on those tables. A DML-only login like the `data_api` mode would be denied
by RLS, because RLS applies to every role except the table owner. Therefore:

- Each database gets one NOLOGIN role, `gregale_authowner_<hash>`, which owns
  the `auth` and `gregale_auth` schemas and nothing else. It has CONNECT but no
  CREATE on the database, no privileges on any other schema, and no membership
  in the main schema owner. Provisioning creates both schemas.
- The `auth` login, `gregale_auth_<hash>`, is NOINHERIT and NOBYPASSRLS. Its
  only membership is the auth owner, and it has `role` set to that owner, as
  migration logins do for the main schema owner. It also has a bounded
  `statement_timeout`. It owns the tables GoTrue creates, so GoTrue's RLS does
  not apply to it. FORCE ROW LEVEL SECURITY is never set on those tables.
- The binding is delivered to both serving processes and release tasks. Its
  DDL authority is limited to the two auth schemas, so delivering it to the
  serving process does not widen the migration boundary for customer schemas.
- Rotation, retirement and RestrictInherited reuse the existing generation
  workflow. RestrictInherited includes the new login prefix.
- The Neon backend advertises `auth` only with `auth_enabled: true` and a
  fresh qualification report that includes an `auth_credentials_probe`. The
  probe proves schema isolation, ownership of the auth schemas, denial of
  `public` and `api`, idempotent issuance and revocation. Existing reports
  without the probe remain valid for their smaller contract.

The template sets `search_path=auth` on the GoTrue connection. That way
GoTrue's `schema_migrations` table is created in `auth` and cannot collide
with a customer migration tool's table in `public`. Upstream migration
`20240612123726` grants SELECT to a role named `postgres`, which a managed
database may not have. The template ships an overlay of that one file that
wraps the grants in a role-existence check. The build fails unless every other
migration file matches the pinned upstream byte for byte.

### Tokens

Access tokens are ES256 JWTs with `sub`, `session_id`, `aal`, `aud` and `exp`.
They live 600 seconds by default; the bound is in `pkg/api/limits.go`, not
GoTrue's 3,600-second default. Refresh tokens rotate on every use. The issuer
is `https://<app host>/auth/v1`.

`data-api create --auth NAME` and `edge-rules create --kind jwt --jwt-auth NAME`
resolve the issuer, JWKS URL and audience from the auth app, in place of the
manual flags; the edge rule also requires ES256 and `exp`. The Data API keeps overwriting the SQL role, so GoTrue's `role` claim
selects nothing.

### Organizations, invitations and roles

The `gregale_auth` schema holds organizations, memberships keyed to
`auth.users` with cascade delete, role definitions mapping a role to
permission strings, and invitations. Invitations are stored as a SHA-256 token
hash, are single use and expire after a bound in `pkg/api/limits.go`. Default
roles are `owner`, `admin` and `member`. The last owner of an organization
cannot be removed or demoted.

The front's `/authz/v1` API creates and lists organizations, lists members,
invites, accepts, revokes invitations, changes roles, removes members and
answers `authorize` checks. GoTrue verifies the caller's token on loopback;
the front then calls one owner-only `gregale_auth` function per request in a
transaction whose `request.jwt.claims` carry only the verified `sub` and
`session_id`. The functions enforce the rules themselves: managing members
needs `members:manage`, only owners grant or remove the owner role, members
may always leave, and changes in one organization are serialized so none loses
its last owner. An invitation token is returned once to the inviter until the
email hook delivers it, is stored only as a hash, and can be accepted only by a
signed-in user whose confirmed email matches the invited address.

Custom roles and policies are declarative: `gregale auth create --authz FILE`
seals an `AUTH_AUTHZ` document (`roles` and `policies`), and the release task
replaces custom roles and every policy from it. The `owner` role always holds
every permission, and a role still assigned cannot be removed.

With `AUTH_ORG_CLAIMS` on, a session chooses the organization it acts in
(`gregale_auth.set_active_organization()`, a member's own session only), and
GoTrue's custom access token hook adds `org_id`, `org_slug`, `org_role` and
`org_permissions` to its first-party tokens, joining the choice with the
current membership and role. The choice is per session, not per user, so
devices and tabs act independently; a foreign key to `auth.sessions` and to
the organization removes it with either. Backends then authorize from a
verified token without a call, accepting that a removed member or changed role
holds until the token expires (`AuthAccessTokenTTLSeconds`); `authorize()`
remains the current check. The hook, which otherwise runs only with the OAuth
server, runs whenever either is on, so apps that use neither pay nothing per
token. Client tokens never carry the claims, and nothing is chosen
implicitly: a user in one organization still sets it, so a token's claims are
never a guess.

### One authorization function

Permissions are resolved in PostgreSQL when access is checked, never copied
into token claims. A role change or revocation therefore takes effect on the
next check, and tokens stay small. The helper functions are:

- `gregale_auth.uid()`
- `gregale_auth.session_active()`, which requires the token's `session_id` to
  still exist in `auth.sessions`, unexpired, for a user who is neither banned
  nor deleted
- `gregale_auth.has_permission(org uuid, permission text)`, which also
  requires an active session
- `gregale_auth.authorize(action text, resource text)`

`authorize` denies by default. It allows an action only for an active session
whose caller holds the permission that a `gregale_auth.policies` row maps to
the action and to the resource prefix. Resources are named
`org/<uuid>/<kind>/<name>`.

The helpers are STABLE and SECURITY DEFINER, owned by the auth owner, with a
fixed `search_path`. They are marked `gregale:auth-helper:v1` and are
executable by PUBLIC. They read only the caller's `request.jwt.claims`, so
they reveal only the caller's own sessions and memberships.

The `read_only` credential verifier rejected every executable SECURITY DEFINER
function. It now allows exactly a function in `gregale_auth`, owned by that
schema's marked auth owner, carrying the helper marker, and STABLE or
IMMUTABLE SQL or PL/pgSQL. PostgreSQL rejects data-modifying statements in such
functions, so calling one cannot give a read-only login writes. Volatile or
unmarked definers still block issuance. The `data_api` verifier inspects only
the `api` and `public` schemas and needs no change.

Any SQL session can set `request.jwt.claims`. The helpers are therefore an
authorization aid for sessions whose claims a verifying gateway set, such as
the Data API's. They do not authenticate direct SQL logins, which the database
owner already trusts.

Each surface reaches the same function:

| Surface | Check |
|---|---|
| Database rows | RLS policies call the helpers through the Data API |
| Object download or upload | `POST /authz/v1/storage/sign` checks `authorize('storage:read'/'storage:write', 'org/<org>/<bucket>/<path>')` and returns a SigV4 URL valid for 60 seconds, signed with a bucket bound to the Auth app |
| Realtime channel | `/realtime/authorize-channel` checks `authorize_subject(subject, 'realtime:read', ...)` for channels named `org:<org>:<kind>:<name>` |
| Workflow start | `POST /authz/v1/workflows/{name}/start` checks `authorize('workflow:start', 'org/<org>/workflow/<name>')` and publishes an app-scoped `auth.workflow.start` event |
| Gateway route | An optional edge JWT rule gates routes coarsely, never as the only check on data |

### Surface checks

Object keys must start with `org/<uuid>/`, and the bucket binding's name is
the policy resource kind. Signing happens inside the Auth app with the
binding's credentials, which never leave it.

Managed realtime passes only the token's subject to its channel callback, not
its `session_id`. Channel checks therefore use an owner-only
`authorize_subject()` that requires the user to be unbanned, undeleted and
holding at least one live session, rather than a specific session. A ban,
deletion or global sign-out stops new subscriptions at the next check. Grants
last for the connection, as ADR-369 specifies; closing open connections on
sign-out is left to the existing connection-close operation.
`gregale realtime create APP --auth AUTH_APP` sets the endpoint's OIDC
issuer, JWKS URL, audience and ES256, points its callback at the Auth app, and
generates the callback token, sealing it only in the Auth app.

Starting a workflow run requires `deploy:write`, which an end-user-facing app
must not hold. The Auth app instead publishes an application-scoped event
(ADR-929) with a key minted by `gregale auth create --workflows` that holds only
`events:publish`. The customer's workflow subscribes to `auth.workflow.start`
from the Auth app's source. The event key includes the user and the caller's
idempotency key, so a retried start returns the original receipt.

### Email

Tenant egress denies ports 25, 465 and 587, so GoTrue's SMTP sender stays off.
GoTrue's send-email hook is always enabled and calls a separate loopback-only
listener in the front (`127.0.0.1:9998`), which the gateway cannot reach. The
front generates a Standard Webhooks secret at each boot and passes it to the
GoTrue child, so nothing needs sealing and a snapshot restores both halves;
each call's HMAC-SHA256 signature and five-minute timestamp are verified.

The front sends through the customer's provider over HTTPS: Resend (with one
idempotency key per hook message, so GoTrue retries do not duplicate mail) or
Postmark, configured by `gregale auth create --email-provider --email-from
--email-api-key-stdin`. Verification, magic-link, recovery and invite emails
link to `/auth/v1/verify`. A secure email change mails the current address
with GoTrue's `token_hash_new` and the new address with `token_hash`. Codes,
reauthentication and security notifications are supported; HTML is escaped.
Gregale operates no sender for application email. Without a provider, email
flows fail with `email_provider_unconfigured`. Links and codes are never
logged.

Owners replace the subject and body of each of eight email types. A template
is plain text with per-type `{{placeholders}}`; the front escapes every value
and builds the HTML itself, so a template cannot carry markup or scripts into
recipients' mail, and an email's link or code is appended when the body does
not place it. Full HTML templates were left out for that reason. Templates live
in the app's database (`gregale_auth.email_templates`), not in app secrets:
they are not secret, the CLI and dashboard must read them back to edit them,
an edit should not need a restart, and Free apps have eight secrets in all.
They are edited through `/admin/v1/emails` with an admin key (CLI and the
dashboard's Emails tab, from the owner's browser), with a preview that renders
example values and can send through the provider. The hook reads them per
email and falls back to the defaults if the database cannot be read.

Organization invitations are emailed when a provider is configured. The send
happens inside the invitation transaction, so a failed send leaves no pending
invitation, and the token is then withheld from the inviter. The link carries
the token in the URL fragment of the hosted invitation page.

### Hosted pages and SDK

The front serves dependency-free pages under `/ui`: sign-in (password, magic
link and the OAuth providers GoTrue reports enabled), sign-up, forgot and
reset password, and invitation acceptance. They hand the session to the
application only in the URL fragment, only to the site URL or a path under an
allowed redirect URL, and never write tokens to storage. They are served with
a `default-src 'none'` content security policy, no framing and no referrer, and
are always in GoTrue's redirect allow list so recovery links return to them.

`@gregale/auth` (`sdk/auth`, restricted publication) wraps sign-up, sign-in,
`sessionFromUrl`, sign-out, user updates and the `/authz/v1` calls. Refresh is
single-flight because refresh tokens rotate, and `getAccessToken` plugs into
`@gregale/data`.

`@gregale/auth/react` (React 18+, an optional peer dependency) puts the same
client behind a provider, hooks and plain-HTML components: an embedded sign-in
form, `SignedIn`/`SignedOut`, a user menu, an organization switcher and
`Authorized`. It treats a user with a verified authenticator app or phone as
signed out until the second step is done, as the hosted pages do, by asking
GoTrue for the user's factors once per aal1 session; that check only decides
what to render, because aal2 is enforced where it matters (RLS, the Auth app).

Native clients (`sdk/swift`, `sdk/kotlin`) mirror it for mobile apps. Sessions
live in the Keychain or the Android Keystore. Returns from the browser use
HTTPS universal links / App Links, because redirect URLs stay HTTPS-only and a
custom scheme can be claimed by another app. OAuth and emailed links from the
app use PKCE with the verifier kept in the session store; emailed codes and
platform ID tokens (Sign in with Apple, Google) need no browser at all.

### Administration and import

Owners administer users through `/admin/v1` on the Auth app with a key they
hold. `gregale auth admin-key create` prints a 256-bit key once and seals only
its SHA-256 as `AUTH_ADMIN_KEY_<id>`; without such a secret the API does not
exist. The dashboard page `/dashboard/apps/{slug}/auth` is served by apid but
its script calls the Auth app directly from the owner's browser: apid adds the
Auth app's exact origin to that page's `connect-src`, and the Auth app answers
CORS only for the Gregale API origin sealed at creation. apid never receives
the key, and user records never pass through or stay in the control plane.
App tasks (ADR-968's owner workspace) were not used, because their output is
kept in apid's catalog and would store application users there.

User changes go through GoTrue's own admin API on loopback with a one-minute
`service_role` token the front signs with the app's ES256 key; GoTrue refuses
HS256 once only asymmetric keys are configured. The public `/auth/v1/admin`
and `/auth/v1/invite` paths are closed, since nothing outside the instance can
mint that token. GoTrue has no admin sign-out, so signing a user out deletes
their sessions, which ends their refresh tokens; a ban does the same. Deleting
the last owner of an organization with other members is refused, and
organizations whose only member was the deleted user go with them.

`gregale auth import` sends at most 100 users per request to
`/admin/v1/users/import`, which creates each through GoTrue with its user ID and
bcrypt, argon2 or Firebase scrypt hash. Plain-text passwords are refused, and
repeated addresses within a batch are skipped because concurrent creates of
one address race inside GoTrue. Responses report rows by position and never
echo addresses.

Custom claims are the keys of a user's `app_metadata` other than GoTrue's
`provider` and `providers`. Only the admin API writes `app_metadata`, and
GoTrue already puts it in every access token, so applications get
Firebase-style per-user claims without a token hook on every refresh.
Dynamic claims computed from the application's own tables were rejected for
now: the Auth binding cannot read customer schemas by design, so a claims
function would need customers to grant a hashed role access to their schema
and a security-definer function. `PATCH /admin/v1/users/{id}/claims`
`{set, unset}` sends GoTrue a merge patch after checking names and
`AuthCustomClaimsMaxBytes` (1,000 bytes, Firebase's bound) against the
user's current claims.

Sign-up restrictions (`AUTH_SIGNUP_ALLOWED_DOMAINS`,
`AUTH_SIGNUP_BLOCKED_DOMAINS`, `AUTH_SIGNUP_BLOCK_DISPOSABLE`,
`AUTH_SIGNUP_ACCESS` open|invite|waitlist) are checked in GoTrue's
before-user-created HTTP hook, which v2.197.0 calls outside its transactions
for every self-service creation (sign-up, OTP, OAuth, SAML, anonymous, Web3
and GoTrue's own invite) and not for admin-created users. The hook runs only
while a restriction is set. Invitation and waitlist access read
`gregale_auth.signup_access` (sql/0018) and pending organization
invitations; a refused waitlist sign-up records a request, bounded by
`AuthWaitlistMaxPending`, rather than adding a public waitlist endpoint that
would need its own CAPTCHA and rate limits. GoTrue returns the hook's
refusal as `msg`, so the hosted pages and SDK read a stable code from it.
The hook fails closed (503). The throwaway list is vendored from the CC0
disposable-email-domains project.

Impersonation (`AUTH_IMPERSONATION`, off by default) lets the admin key
open a session as a user. GoTrue v2.197.0 has no administrator sign-in, so
the front generates a magic link through the admin API and redeems it
server-side, which yields a real session that every authorization surface
already understands; minting tokens ourselves would lack the session row
that `session_active()` requires. The front then caps the session with
`auth.sessions.not_after` (5 to 60 minutes), records it in
`gregale_auth.impersonations` (sql/0019) with the key's ID and the reason,
and refreshes once so the access token hook adds `act` before the session is
handed over. Account changes are refused at the front from tokens with
`act`, by path, before GoTrue sees them; dropping `act` would break the
token's signature, so reading the unverified payload there is safe. Admin
keys now carry their IDs so the record names the key that acted.

### Lifecycle events and activity

`gregale auth config --events on` sets `AUTH_LIFECYCLE_EVENTS` and, if needed,
mints the `events:publish`-only key. Triggers on `auth.users` and
`auth.sessions` record `auth.user.created`, `confirmed`, `signed_in`, `banned`,
`unbanned` and `deleted` in a `gregale_auth` outbox, in the same transaction
as the change, so no path into GoTrue (sign-up, OAuth, invitation, import,
administration) is missed. Rows hold the user ID and non-personal facts only,
because published events are stored in Gregale's control plane; consumers read
anything else from their own database. The triggers read optional GoTrue
columns through `to_jsonb`, and record nothing until the front enables them at
startup; turning events off clears the outbox.

The front publishes the outbox through the app-scoped producer API (ADR-929),
keyed by row ID so retries deduplicate. It drains after requests that may
change users and at startup, never by polling, so an idle Auth app neither
wakes nor keeps a scale-to-zero database awake. A claim leases rows, so
instances draining together do not publish twice; failures back off up to an
hour, and rows older than three days are dropped (`pkg/api/limits.go`).

`GET /admin/v1/activity` reads GoTrue's audit log, newest first, overall or
for one user, through the owner's admin key; the dashboard's Activity tab, the
user panel and `gregale auth activity` use it. GoTrue records an
administrator's action with the zero actor ID and the affected user in its
traits, so such entries are reported against that user and marked as an
administrator's. With `AUTH_AUDIT_RETENTION_DAYS` the front deletes older
entries in batches, after requests and at most every six hours, never on a
timer; the dashboard's CSV export is built in the owner's browser from these
pages. The release task adds indexes on time, actor and affected
user, because GoTrue indexes the log only by instance. GoTrue v2.197.0 leaves
the log's IP address empty, so none is shown.

### Passkeys, branding and CAPTCHA

GoTrue v2.197.0 implements passkeys (`GOTRUE_PASSKEY_ENABLED`) with
discoverable credentials. Gregale owns the WebAuthn relying party: its ID is
the Auth app's host, or a parent domain the owner names
(`AUTH_PASSKEY_RP_ID`) that the Auth app's host lies under, so a custom-domain
Auth app and its site share passkeys. The allowed origins are the HTTPS
origins among the Auth app, site and redirect URLs on that domain. The hosted
pages sign in with a passkey and offer to add one after a password sign-in or
sign-up; `@gregale/auth` registers, lists, renames and removes them.

The hosted pages show a name (also used in emails and passkey prompts), an
accent colour served as `/ui/brand.css`, and a logo from an HTTPS URL. A logo
is loaded by URL, not stored, because a free plan's secrets are too small to
hold one, and the pages' policy allows only that URL's origin for images.

CAPTCHA (Cloudflare Turnstile or hCaptcha) is a typed setting. GoTrue verifies
the token on sign-up, password and magic-link sign-in, recovery and the start
of a passkey sign-in; the hosted pages render the provider's widget and add a
fresh token to each of those requests. Only when CAPTCHA is on does the pages'
policy allow the provider's script, frame, style and connection origins.

### Two-step verification

GoTrue's factors are typed settings: TOTP (on by default), SMS codes and
recovery codes, each with enrolment switchable and verification left on so
enrolled users are never locked out. SMS codes need the SMS provider; without
one the Auth app keeps their enrolment off instead of creating factors that
no code can reach. The hosted pages complete a verified second factor after
every first-factor sign-in they perform, so they never hand an application an
`aal1` session for a user who has one; `@gregale/auth` exposes enrolment,
challenge, verification, recovery codes and the assurance level. Applications
enforce `aal2` themselves, through the `aal` claim. GoTrue texts a phone
factor's challenge through the SMS provider even for numbers in
`GOTRUE_SMS_TEST_OTP`, which covers sign-in codes only.

The hosted account page `/ui/account` gives people the self-service that
GoTrue's user API allows without each application building it: email and
password changes, second factors and recovery codes, passkeys, linked
providers, connected OAuth apps and their own sessions. Like the consent page
it signs in through the usual pages (so the session is `aal2` for anyone with
a second factor, which removing a factor requires), keeps the session only in
the tab's memory and ends it on leaving; it never reuses the application's
session, which would need a token handed across origins. GoTrue v2.197.0 can
end the current session, the others or all, but cannot list sessions or end
one, so `gregale_auth.list_sessions()` and `end_session()` (owner-only, run by
the front under the caller's verified claims like the organization
functions) serve `/authz/v1/sessions`. They leave third-party OAuth clients'
sessions to connected apps, and the current session to GoTrue's logout. The
page's image policy adds `data:` for the QR code GoTrue returns inline.

Session limits are typed settings over GoTrue's own: `AUTH_SESSION_MAX_AGE_MINUTES`
becomes `GOTRUE_SESSIONS_TIMEBOX`, `AUTH_SESSION_IDLE_MINUTES` the inactivity
timeout and `AUTH_SINGLE_SESSION` `GOTRUE_SESSIONS_SINGLE_PER_USER`, which keeps a
user's most recently refreshed session and refuses the others at their next
refresh. GoTrue checks all three when a session refreshes, so they are bounded
from `AuthSessionMinMinutes` (above the access token lifetime) to
`AuthSessionMaxMinutes`, and an issued token outlives its session by at most
`AuthAccessTokenTTLSeconds`, as after a sign-out. `GOTRUE_SESSIONS_*` stays
managed, not passed through. An idle limit above the maximum age is accepted
and has no effect: the two are set separately, and refusing the combination
at start would take the app down.

GoTrue has no endpoint for people to delete themselves. With
`AUTH_SELF_DELETION` on (off by default: an app may need to settle billing
or data first), `DELETE /authz/v1/account` deletes the caller through the
same path as an owner's deletion, so the last-owner refusal and the removal
of organizations left without members are shared. It requires the token's
session to be younger than `AuthReauthenticationSeconds` and, for a user
with a verified second factor, `aal2`; anonymous users are exempt because
they cannot sign in again. A third-party client's token cannot reach it.
GoTrue's audit log records the deletion as an administrator's, since it goes
through the admin API; the lifecycle event `auth.user.deleted` is the same
either way.

Wallet sign-in uses GoTrue v2.197.0's `web3` grant
(`GOTRUE_EXTERNAL_WEB3_ETHEREUM_ENABLED`, `_SOLANA_ENABLED`, set by
`gregale auth config --web3`): GoTrue parses the EIP-4361 or Solana message,
verifies the signature and requires the message's URI to be an allowed
redirect URL and its domain to be that URI's host. The hosted pages sign on
the Auth app's own host, so while Web3 is on its root is added to
`GOTRUE_URI_ALLOW_LIST`. `@gregale/auth` builds the message in the grammar
GoTrue's parsers accept and asks an injected wallet (EIP-1193 or Solana) to
sign it. GoTrue keeps no record of nonces, so a signed message is valid for
its ten-minute window.

An organization can require a second factor of its members
(`organizations.mfa_required`, sql/0016). The check lives in
`has_permission`, so every surface that already goes through it (row-level
security via `authorize()`, storage signing, workflows, realtime channels and
the organization functions) refuses sessions below `aal2` for that
organization without its own code. It reads the session row, not the token's
`aal` claim: GoTrue lowers a session to `aal1` when its factor is removed,
while tokens issued earlier keep saying `aal2` until they expire. The access
token hook does the opposite and reads the claim, because GoTrue calls it
inside the transaction that raises the session, before the row shows `aal2`;
below `aal2` it leaves such an organization's claims out. Members are not
removed: they keep the membership, still list the organization (with
`mfa_required`), and get `mfa_required` instead of `forbidden` from the
organization API so an application can send them to verify. Changing the
setting needs `mfa:manage` and an `aal2` session in both directions, so
turning it on cannot lock out the person doing it and a session that skipped
the second factor cannot turn it off; the application's operator can change
it through the admin API without being a member. Members with `members:manage`
see which members have a verified second factor. The subject-level
`authorize_subject` cannot see a session, so for such an organization it needs
the user to hold some live `aal2` session. Single sign-on counts as one factor:
GoTrue does not see the identity provider's own second step.

### Phone sign-in

Phone sign-in uses GoTrue's own SMS providers (Twilio, Twilio Verify,
MessageBird, Textlocal and Vonage), which call the provider's HTTPS API with
the customer's account, so tenant egress needs no exception and Gregale sells
no messages. `GOTRUE_SMS_*` secrets join the pass-through settings, and a
typed setting writes one provider's account, sender and secret and removes
every other provider's. Codes last five minutes rather than GoTrue's 60
seconds, which a delayed text can outlast, and the message names the app.
Texting costs the customer money and attracts SMS pumping, so the CLI warns
when phone sign-in is on without CAPTCHA, and `AUTH_SMS_COUNTRIES` limits the
country calling codes the app texts: the front reads every request that can
text a number (code sign-in, sign-up, resend, phone change, phone MFA
enrolment), normalises the number exactly as GoTrue does, checks every JSON
key GoTrue would read as the phone (Go matches field names without regard to
case) and refuses the rest before GoTrue sees them; GoTrue's per-instance limit of 30
messages an hour and one code a minute per number also apply. A confirmed
phone number publishes `auth.user.confirmed` with `channel: phone`; the
number itself is never in an event.

### Organization single sign-on

Organization SSO uses GoTrue's SAML 2.0 service provider. `AUTH_SSO` turns it
on; `AUTH_SAML_PRIVATE_KEY` is the RSA key GoTrue signs with and publishes in
its metadata. The CLI or dashboard creates the key the first time SSO is
turned on and never replaces it, because a new key would make every
identity provider distrust the app; turning SSO off keeps it sealed. The owner
registers identity providers with an admin key through `/admin/v1/sso` (from
the CLI or the dashboard's SSO tab, which calls it from the owner's browser),
which calls GoTrue's admin API, so provider records stay in the customer's
database like users. Each provider must name its email domains, and may be
linked to one organization (`gregale_auth.sso_organizations`) with a role
other than owner: a trigger on `auth.identities` adds a user who signs in
through it for the first time, and linking adds its existing users. Members
keep their memberships when a link changes or ends.

GoTrue keeps SSO accounts separate from other accounts with the same address,
so an identity provider cannot sign in as an existing password or OAuth user.
It could still assert any address for its own users, so an SSO user may
accept an organization invitation only for an address in one of its
provider's domains.

Self-service SSO for an organization's own admins is opt-in
(`AUTH_ORG_SSO`, which needs SSO). It requires proof of domain control: the
organization adds a domain, publishes the returned token as a TXT record at
`_gregale-auth.<domain>`, and the Auth app looks it up itself; a domain is
verified by at most one organization (a partial unique index), so one
organization cannot claim another's domain. Owners and members with
`sso:manage` then register up to three providers for verified domains only,
through `/authz/v1/orgs/{org}/sso`, which checks the rules in SQL
(`check_org_sso`) before calling GoTrue's admin API and links the provider
to the organization; a failed link removes the provider again. A domain an
organization's provider serves cannot be removed.

SCIM provisioning is served by the front, because GoTrue v2.197.0's SCIM
module is an experimental stub with discovery endpoints only. Each SSO
provider may have one SCIM bearer token, stored as a SHA-256 hash; requests
are scoped to that provider. `/scim/v2/Users` keeps a provisioning record
(`gregale_auth.scim_users`) rather than creating GoTrue users, because GoTrue
keeps SSO accounts separate and a pre-created user would never be the one the
person signs in as. A trigger on `auth.identities` links the record on the
person's first SSO sign-in (it runs before the organization join), and bans
a person who was deprovisioned first. Deprovisioning a linked person bans them
through GoTrue's admin API, ends their sessions and removes their membership in
the provider's organization, never its last owner; deleting the record does the
same rather than deleting the account. Groups are not supported: the
provider's organization link decides membership.

Each organization has an audit log (sql/0020) that its owners, or roles
granted `audit:read`, read through `/authz/v1/orgs/{org}/audit-log`.
Triggers on the organization tables record changes in the same transaction
as the change, whichever path made it (the organization API, the admin key,
SSO and SCIM joins inside GoTrue's transactions, domain joining), so no
surface can skip the record; the actor is the verified caller the front
set in `request.jwt.claims`, and null otherwise. A trigger skips an
organization that is being deleted, so cascades cannot fail. Members'
sign-ins are read from GoTrue's audit log at query time, for current
members, rather than copied per organization. Entries follow the app's
activity retention.

Organizations can also let people join by email domain
(`AUTH_ORG_DOMAINS`, sql/0017), reusing the TXT-verified domains of
self-service SSO without needing SSO. Joining is 'suggest' (the person
chooses, from the API or the hosted account page) or 'automatic' (triggers on
`auth.users` and SSO identities add people when their address is confirmed or
changes, and turning it on adds those already there). It trusts only proven
addresses: confirmed while the app confirms addresses, which the front writes
to `domain_join_settings` at startup because the triggers run inside GoTrue's
transactions and cannot see the app's settings. With `AUTH_AUTOCONFIRM` on,
anyone could claim any address, so joining stays off; the dashboard and a
single `auth config` change refuse the combination, and status reports it. SSO users count only for their provider's
domains, as with invitations. `membership_history` records every membership
ever made, so domain joining adds a person once and never undoes an owner's
removal; the triggers do nothing that can fail inside GoTrue's transaction.
Domains are managed with `domains:manage` or, as before, `sso:manage`.

### Dark launch

Application Auth reaches no customer until it launches. apid reads
`FAAS_APPLICATION_AUTH_ENABLED` (every account) and
`FAAS_APPLICATION_AUTH_ACCOUNTS` (a comma-separated allowlist for internal
accounts) on every request (`api.ApplicationAuthEnabled`). Where it is off,
the `auth` credential mode is indistinguishable from an unknown one in the
binding API, the deploy manifest and capability discovery; the dashboard's
Auth link and screens do not exist; and the template catalog omits `auth`.
Existing Auth apps keep running. The CLI shows the `auth` command, the
`--auth`/`--jwt-auth` flags of other commands, the `auth` access mode and
template only with `FAAS_APPLICATION_AUTH_ENABLED` set in the shell; without it
they are absent from help, completions, man pages, the generated reference
and suggestions, and dispatch treats them as unknown. The customer docs omit
the Auth page (`docs/application-auth.md`, unlisted) and the public OpenAPI
omits the mode. Launch reverses these: add the page to
`docs/customer-pages.json`, the mode to the OpenAPI enums and the managed
Postgres page, drop the `Unreleased` markers and the `unlisted` entry, and set
the apid flag.

### OAuth 2.1 authorization server

GoTrue v2.197.0 is also an OAuth 2.1 authorization server: authorization code
with PKCE, refresh tokens, OpenID Connect ID tokens, RFC 8414 metadata and
RFC 7591 registration. `AUTH_OAUTH_SERVER` turns it on so third-party apps
and MCP clients can act for an application's users with their consent;
`AUTH_OAUTH_REGISTRATION` separately lets clients register themselves, as
MCP clients do. Owners register clients with an admin key through
`/admin/v1/oauth/clients` (`gregale auth oauth-clients`); one app holds at
most `AuthOAuthMaxClients`, and the front refuses self-registration beyond
that.

GoTrue sends people to `SITE_URL` plus a configured path to approve a
client. The front redirects that to the hosted consent page on the Auth
host, which signs the person in with the usual pages, shows the client's
self-chosen name, the permissions, the requested resource and the host
they return to, and ends the session it made once they decide. The issuer
`<host>/auth/v1` is discoverable at the RFC 8414 path-insertion URL.

GoTrue gives a client's access token the same audience and powers as the
user's own session, and lets it call every user endpoint, including changing
the email address or password. The front therefore confines such tokens
(those with a `client_id` claim): on the Auth host they reach only userinfo,
their own sign-out and the permission check `/authz/v1/authorize`, which a
resource server needs; everything else answers `oauth_client_token`.
GoTrue's custom access token hook, on only with the server, sets their
audience to the RFC 8707 resource the client was authorized for, or
`oauth-client` when it named none. GoTrue drops the authorization at the
code exchange, so the hook records each client session's resource in
`gregale_auth.oauth_session_resources` and refreshed tokens keep it. A
verifier set up for the app's own tokens (`authenticated`: the Data API,
edge rules, realtime and the SDKs by default) never accepts a client's
token; an API or MCP server accepts one for its own resource
(`gregale mcp init --auth-app`, the SDKs' audience option). GoTrue issues
only the OpenID scopes, so finer permissions come from `authorize()`.

### Anonymous users and identity linking

GoTrue's anonymous users let people start, for example fill a cart, before
creating an account. They are a typed setting
(`GOTRUE_EXTERNAL_ANONYMOUS_USERS_ENABLED`); GoTrue rate-limits them per
client address and checks CAPTCHA on their sign-up, and the CLI advises
turning CAPTCHA on with them. Their tokens carry `is_anonymous`, which the
SDKs expose and RLS policies can read. Anyone can become one without an
address, so `create_organization` refuses them (`anonymous_user`); they hold
no memberships, so `authorize()` grants them nothing.

An anonymous user becomes permanent, keeping its ID and data, by adding an
email address and password, a phone, or a provider account. Linking a
provider, like unlinking any sign-in method, needs `AUTH_IDENTITY_LINKING`
(GoTrue's manual linking), which Gregale owns. GoTrue never deletes anonymous
users, and on a database the customer pays for they accumulate; with
`AUTH_ANONYMOUS_RETENTION_DAYS` the front deletes those created, and with no
session refreshed, within that many days, in the same request-driven batches
as activity retention. Deleting one ends its sessions, identities and
memberships; the application's own tables decide through their foreign keys.

### Scale to zero

The gateway, Data API and realtime service verify tokens against cached JWKS,
and so do the Go, Python and Node backend SDKs, with the same five-minute cache and
at most one refetch every 30 seconds for an unknown key ID. A parked auth app
therefore wakes only for sign-in, refresh, administration,
organization changes and the front's storage, realtime and workflow checks.

Sign-out and bans take effect on the next check that calls
`session_active()`. Edge-only JWT rules honour them when the access token
expires, at most 600 seconds later by default.

### Ownership, quotas and data

apid remains the only writer of intent: the auth app, its binding and secrets
are ordinary resources. End-user records live only in the customer's database.
Gregale's catalog stores no application user data, and an export is the
customer's own `pg_dump`.

The auth app is an ordinary deployed app for quota and RAM billing, as the
Data API is. This ADR adds no plan quota or meter. Auth-specific bounds live
in `pkg/api/limits.go`.

## Phases

1. The `auth` credential mode: provisioning, verification, qualification
   probe, binding constraints, delivery policy, API enums and CLI modes.
2. The `auth` template, `gregale auth create|status`, and the migration
   overlay check. Signing-key rotation (`gregale auth keys`) and typed settings
   (`gregale auth config|providers` and the dashboard) followed, then
   lifecycle events and the activity view. The dashboard later gained email
   delivery and signing-key rotation, with the CLI's rules shared through
   `pkg/authsettings`.
3. The `gregale_auth` schema and helpers, the verifier allowance, the
   `/authz/v1` API, declarative `AUTH_AUTHZ` roles and policies, and `--auth`
   for the Data API and edge JWT rules.
4. Hosted pages, the `@gregale/auth` SDK and the email hook. Go and Python
   backend SDKs (`sdk/go/auth`, `faas_sdk.auth`, `@gregale/auth/server`)
   followed.
5. The storage, realtime and workflow checks.
6. Dashboard user and organization screens through the auth app's admin API
   with an owner-held key, user import, and native acceptance.

## Rejected alternatives

- **Building identity in-house:** months of security-critical work that a
  maintained MIT server already does.
- **A platform `authd` daemon serving every customer:** it would put
  application user data and every customer's sign-in path in the control
  plane, and users could not be joined in RLS policies.
- **Ory Kratos with Keto:** several services per app and a permission store
  outside PostgreSQL.
- **Keycloak:** its Java runtime's memory footprint conflicts with
  scale-to-zero economics.
- **A hosted partner such as Clerk or WorkOS:** users leave the customer's
  database, and costs scale per user.
- **A `read_write` credential for GoTrue:** RLS blocks non-owners on GoTrue's
  tables, and the credential would reach every customer table.
- **Permissions in token claims:** stale after role changes and unbounded in
  size.
- **OAuth client tokens with the app's own audience:** GoTrue's default; any
  consent, including to a self-registered client, would grant the user's
  full Data API and Auth API powers.

## Acceptance

PostgreSQL tests cover isolation, ownership, idempotency, rotation and
revocation of the `auth` mode. All 75 GoTrue v2.197.0 migrations have been
replayed through an `auth` login on PostgreSQL 16: the 16 RLS-enabled tables
belong to the auth owner and accept its writes. The built template image has
run against that credential on PostgreSQL 16: the release task applied every
migration, sign-up issued an ES256 token that verifies against the published
JWKS with the expected issuer, audience and 600-second lifetime, refresh
tokens rotated, and sign-out ended refresh. Under amd64 emulation the instance
used about 131 MiB (Node ~62 MB, GoTrue ~84 MB RSS), above the Free plan's
128 MB. Measured natively on x86_64 (fsn-4, 2026-10-17), the instance idles
at 98 MiB RSS (Node 64, GoTrue 33; 33 MiB of it anonymous) and ran every SMS,
second-factor, SAML, SCIM and organization-SSO check inside the Free plan's
136 MiB container cgroup without an OOM kill, so the front stays in Node.
GoTrue hashes each recovery code with argon2id (19 MiB), which grew its RSS to
98 MiB unbounded; the front starts it with `GOMEMLIMIT` at a third of the
instance's memory, which cut the cgroup's `memory.max` events over the same
run from 1004 to 308. The microVM measurement on a Free instance stays part of
the staging canary. With phase 3 the release
task also applied the `gregale_auth` SQL and an `AUTH_AUTHZ` document, twice
idempotently; read-only issuance still verified next to the real schema; and
two real users created an organization, invited, accepted, were authorized by a
declared policy, and lost access at the next check after sign-out (about
134 MiB under emulation). With phase 4, GoTrue's own signed send-email hook
calls reached the loopback listener and passed verification, sign-up and
magic links reported `email_provider_unconfigured` without a provider, and the
container logs held no user email addresses. Provider delivery is covered by
request-shape tests; live Resend and Postmark sends are still owed. Phase 5
is covered by PostgreSQL tests of `authorize_subject()` (member, non-member,
ban, no live session, owner-only), a SigV4 test against AWS's published
presigned-URL example, and a run against the built image: a signed object URL
for a member, a refused write without a write policy, realtime grants for a
member and refusals for a non-member and a forged callback token, a workflow
start event with the events key, and a realtime refusal after global sign-out.
With phase 6 the built image imported a bcrypt-hashed user who then signed in
through GoTrue with the original user ID; listing, detail, ban (authorization
and sign-in refused), unban and delete (removing an organization only that user
belonged to) worked through `/admin/v1`; and the public GoTrue admin path
returned 404. Native acceptance must show sign-up, Data
API RLS and session revocation through a builder-built deployment, with wake
latency, resident memory and snapshot size within plan limits.
`make auth-staging-canary` (tests/auth/staging) implements that run against an
isolated staging account; it has not yet run, and no staging target exists
yet. This ADR alone changes no production gate.

On 2026-10-20 the image built from `main` (edefb05a19), with every feature in
this ADR merged, was requalified natively on x86_64 (gregale-internal-test-1,
since fsn-4 was unavailable). The 2026-10-17 run passed again (47 checks: SMS,
second factors, SAML through Keycloak, SCIM, organization SSO), as did the
OAuth server (37) and anonymous users with retention (12). Under the Free
plan's 136 MiB container cgroup the 47 checks ran with a 68 MiB peak and no
`memory.max` or OOM events. The checks written for the later features ran
against the same image with stand-ins for Resend, Twilio and DNS: custom
claims (11), sign-up restrictions (17), impersonation (21), organization
claims, the organization audit log (6), organization-required MFA (24), domain
join with real TXT resolution (25, plus 5 under autoconfirm), Web3 (10),
self-deletion, email templates and session limits (6). That run found the
account page listing sessions GoTrue had revoked under one session per user;
the list now keeps only the live one.
