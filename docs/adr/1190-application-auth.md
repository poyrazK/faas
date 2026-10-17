# ADR-1190: Application Auth on a pinned GoTrue server

- **Status:** proposed; phases 1–6 implemented (the `auth` credential mode; the template and `gregale auth create|status`; organizations, roles, `authorize()` and `--auth`; email, hosted pages and `@gregale/auth`; storage, realtime and workflow checks; owner administration, the dashboard screens and user import; signing-key rotation and typed settings; lifecycle events and the activity view; passkeys, branded hosted pages, CAPTCHA and activity retention; Go, Python and Node backend SDKs; phone sign-in by SMS; organization SAML single sign-on), not yet qualified on a live backend or native builder
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
registers identity providers with an admin key through `/admin/v1/sso`, which
calls GoTrue's admin API, so provider records stay in the customer's
database like users. Each provider must name its email domains, and may be
linked to one organization (`gregale_auth.sso_organizations`) with a role
other than owner: a trigger on `auth.identities` adds a user who signs in
through it for the first time, and linking adds its existing users. Members
keep their memberships when a link changes or ends.

GoTrue keeps SSO accounts separate from other accounts with the same address,
so an identity provider cannot sign in as an existing password or OAuth user.
It could still assert any address for its own users, so an SSO user may
accept an organization invitation only for an address in one of its
provider's domains. Self-service SSO for an organization's own admins is
rejected for now: it needs domain ownership proof, without which one
organization could claim another's domain.

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
128 MB. Native x86_64 measurement decides whether the front must move from
Node to a small Go process before Free-plan availability. With phase 3 the release
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
isolated staging account; it has not yet run. This ADR alone changes no
production gate.
