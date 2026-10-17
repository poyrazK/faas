# Application Auth

Application Auth gives your application its own users: sign-up, sign-in,
sessions, token refresh, sign-out, MFA and OAuth providers. It is separate from
your Gregale account login (`gregale login`). The design is
[ADR-1190](adr/1190-application-auth.md); this page covers what ships today.

## Create it

Start with a ready managed database whose backend advertises the `auth`
credential mode.

```sh
gregale auth create shop-auth --database shop-db --site-url https://shop.example.com
gregale auth status shop-auth
```

The command reserves an ordinary app, attaches an `auth` database binding,
generates an ES256 signing key and seals it with the other settings, and
deploys a pinned GoTrue (Supabase Auth) v2.197.0 server through the normal
builder and deploy path. Repeat with `--resume` to continue after a failure;
existing signing keys are never replaced.

| Flag | Meaning |
| --- | --- |
| `--site-url` | Where users return after sign-in. HTTPS, or HTTP on a loopback host. |
| `--redirect-urls` | Additional allowed redirect targets, comma-separated (at most 50). |
| `--autoconfirm` | Sign users in without email confirmation. Development only. |
| `--disable-signup` | Only invited or administrator-created users can sign in. |
| `--scope` | Environment scope for the binding, secrets and deployment. |

## Use the tokens

The API is served under `https://<auth-host>/auth/v1`. Access tokens are ES256
JWTs with `sub`, `session_id`, `aud=authenticated` and a 600-second lifetime;
refresh tokens rotate on every use. GoTrue's API is compatible with the
`supabase-js` auth client pointed at that URL.

Verify the tokens wherever they are used:

- **Data API:** `gregale data-api create NAME --database DB --auth shop-auth`,
  then write row-level security policies (see below).
- **Edge JWT rules:** `gregale edge-rules create --app APP --kind jwt
  --match-host HOST --jwt-auth shop-auth` requires a valid token on a route.

Sign-out and bans end refresh immediately. A token already issued stays valid
for edge-only checks until it expires, at most 600 seconds later.

## Your data

Users, sessions and identities live in the `auth` schema of your own database,
so a `pg_dump` exports them. The `auth` binding acts as the owner of only the
`auth` and `gregale_auth` schemas: it cannot read or change your other tables,
and your runtime and read-only bindings cannot read `auth`. Creation fails if
your database already has an `auth` schema owned by another role.

GoTrue migrations run in the deployment's release task. The image checks every
pinned upstream migration byte for byte. The release runner verifies the raw
files again, applies the overlay for a migration whose grants assumed a role
named `postgres`, and records progress in `auth.schema_migrations`. Each file and
its version record commit together; retries skip recorded versions. GoTrue's
stock migration command uses embedded SQL and cannot apply this overlay, so the
release runner applies the verified files directly. Serving uses `auth serve`
and performs no migration.

## Change settings

`gregale auth config shop-auth` shows the running app's settings. Add flags to
change them; the app restarts to apply the change. The dashboard's Auth page
(`/dashboard/apps/shop-auth/auth`) shows and edits the same settings and
providers.

```sh
gregale auth config shop-auth --password-min-length 12 --password-require lower,upper,digit
gregale auth config shop-auth --signup off --mfa on --leaked-password-check on
gregale auth config shop-auth --email-provider resend --email-from 'Shop <auth@shop.example.com>' --email-api-key-stdin
```

| Flag | Meaning |
| --- | --- |
| `--site-url`, `--redirect-urls` | Where users return after sign-in; `--redirect-urls none` clears the list. |
| `--signup on\|off` | Whether anyone may sign up, or only invited and administrator-created users. |
| `--autoconfirm on\|off` | Skip email confirmation. Development only. |
| `--password-min-length N` | Minimum password length, 6 to 72. |
| `--password-require CLASSES` | `lower`, `upper`, `digit` and `symbol`, comma-separated, or `none`. |
| `--leaked-password-check on\|off` | Refuse passwords found in known breaches (Have I Been Pwned). |
| `--mfa on\|off` | Let users enrol an authenticator app (TOTP). `off` stops new enrolments; enrolled users keep verifying. |
| `--email-provider`, `--email-from`, `--email-api-key-stdin` | HTTPS email delivery (see Email); `--email-provider none` removes it. |
| `--authz FILE` | Replace custom roles and policies (see Organizations and roles). |

Settings are app secrets, which are write-only, so the CLI and the dashboard
read them back from the running app's public `/settings/v1`. It reports only
values that are public anyway or harmless: URLs, enabled providers, password
rules, whether MFA and email are on, and the signing key IDs.

### Sign-in providers

```sh
gregale auth providers enable shop-auth google --client-id ID --client-secret-stdin
gregale auth providers shop-auth
gregale auth providers disable shop-auth google
```

Register `https://<auth-host>/auth/v1/callback` as the callback URL with the
provider; the Auth app sets it for every enabled provider. Supported:
`apple`, `azure`, `bitbucket`, `discord`, `facebook`, `figma`, `github`,
`gitlab`, `google`, `kakao`, `keycloak` (requires `--url`), `linkedin_oidc`,
`notion`, `slack_oidc`, `spotify`, `twitch`, `workos`, `x` and `zoom`. `--url`
also points `gitlab`, `azure` or `workos` at a self-hosted server or tenant.
Enabling replaces the provider's credentials; disabling removes them. The
hosted sign-in page shows a button for each enabled provider.

### Other GoTrue settings

Other `GOTRUE_EXTERNAL_*`, `GOTRUE_MFA_*`, `GOTRUE_PASSWORD_*` and
`GOTRUE_SECURITY_CAPTCHA_*` settings can still be set as app secrets. Any other
`GOTRUE_*` secret stops startup, because Gregale owns that setting. The hosted
pages do not render a CAPTCHA, so enabling one blocks sign-up there.

## Rotate signing keys

Each ES256 key is its own sealed secret. One key signs; up to three others
only verify, so tokens they signed stay valid. Rotate without signing anyone
out:

```sh
gregale auth keys rotate shop-auth
gregale auth keys list shop-auth
gregale auth keys revoke shop-auth 0A1B2C3D   # the previous key, ten minutes later
```

`rotate` adds a standby key and waits until the restarted app publishes it in
its JWKS, so every verifier can learn it before it signs. Then it makes the
key sign and waits again. The step-by-step commands are `keys create` (standby)
and `keys activate KEY_ID`. The previous key keeps verifying the tokens it
signed, which expire within 600 seconds, so revoke it afterwards. Revoking a
key that signs, or is being made the signer, is refused.

To cut off a leaked key, rotate and revoke the old key at once. Its tokens stop
verifying as soon as the Auth app restarts, and in Gregale's gateway, Data API
and realtime within 5 minutes, when they refresh the JWKS. Signed-in users keep
their sessions: their next refresh returns tokens from the new key.

## Organizations and roles

Every user can create organizations and invite others. The built-in roles are
`owner` (every permission), `admin` (`members:manage`, `org:read`) and `member`
(`org:read`). Declare custom roles and the policies that map actions to
permissions in a JSON file, and apply it on the next deploy:

```json
{
  "roles": { "editor": ["org:read", "doc:write"] },
  "policies": [
    { "action": "read", "resource": "doc", "permission": "org:read" },
    { "action": "write", "resource": "doc", "permission": "doc:write" }
  ]
}
```

```sh
gregale auth create shop-auth --database shop-db --site-url https://shop.example.com --resume --authz authz.json
```

The file is the whole truth: custom roles and policies missing from it are
removed, and removing a role that is still assigned fails the deploy.

Call the organization API with the user's access token
(`Authorization: Bearer <token>`). Browsers may call it from the site and
redirect URL origins.

| Method and path | Effect |
| --- | --- |
| `POST /authz/v1/orgs` `{slug, name}` | Create an organization; the caller becomes its owner |
| `GET /authz/v1/orgs` | The caller's organizations and roles |
| `GET /authz/v1/orgs/{org}/members` | Members (needs `org:read`) |
| `PATCH /authz/v1/orgs/{org}/members/{user}` `{role}` | Change a role (needs `members:manage`; owners only for `owner`) |
| `DELETE /authz/v1/orgs/{org}/members/{user}` | Remove a member, or leave |
| `POST /authz/v1/orgs/{org}/invitations` `{email, role, expires_in?}` | Invite; returns the token once |
| `GET`/`DELETE /authz/v1/orgs/{org}/invitations[/{id}]` | List or revoke pending invitations |
| `POST /authz/v1/invitations/accept` `{token}` | Join, if the caller's confirmed email was invited |
| `POST /authz/v1/authorize` `{action, resource}` | `{"allowed": true or false}` |

No organization can lose its last owner. Invitations last seven days by default
(300 seconds to 14 days); a user can create up to 100 organizations, and an
organization can hold 500 pending invitations.

## Authorize anywhere with one function

Name resources `org/<organization id>/<kind>/<name>`. `gregale_auth.authorize`
allows an action only when a policy maps it on that kind of resource to a
permission the caller holds in that organization, and only while the caller's
session is active, so sign-out and bans apply on the next check. Use it in
row-level security through the Data API:

```sql
ALTER TABLE api.docs ENABLE ROW LEVEL SECURITY;
CREATE POLICY docs_read ON api.docs
  USING (gregale_auth.authorize('read', 'org/' || org_id || '/doc/' || id));
```

`gregale_auth.has_permission(org, permission)` checks a permission directly.
Both read only the caller's verified token claims; your other bindings,
including read-only ones, can call them but cannot read the organization tables.

## Email

Application email goes through your own provider over HTTPS; Gregale sends
none itself, and tenant egress blocks SMTP.

```sh
printf '%s' "$RESEND_API_KEY" | gregale auth create shop-auth --database shop-db --site-url https://shop.example.com --resume --email-provider resend --email-from "Shop <auth@shop.example.com>" --email-api-key-stdin
```

Use `postmark` with a Postmark server token the same way. Confirmation,
magic-link, password-reset and invitation emails then link back to the Auth
app, and organization invitations are emailed instead of returned to the
inviter. Without a provider, flows that need email fail with
`email_provider_unconfigured`; use OAuth providers, or `--autoconfirm` during
development. Links and codes are never logged.

## Hosted pages

Send users to `https://<auth-host>/ui/sign-in?redirect_to=<your URL>`. The
pages handle sign-in (password, magic link, enabled OAuth providers), sign-up,
password reset and invitations, then return to `redirect_to` with the session
in the URL fragment. Only your site URL and paths under `--redirect-urls` are
accepted as targets. Read the session with `@gregale/auth`:

```ts
import { createAuthClient } from '@gregale/auth'

const auth = createAuthClient({ url: 'https://shop-auth.gregale.app' })
await auth.sessionFromUrl()
const token = await auth.getAccessToken()
```

## Files, realtime channels and workflows

Each check names a resource in an organization and goes through the same
policies.

**Files.** Bind a bucket to the Auth app, then declare who may read or write:

```sh
gregale add bucket uploads --app shop-auth --permission read_write
```

```json
{ "policies": [
  { "action": "storage:read", "resource": "uploads", "permission": "org:read" },
  { "action": "storage:write", "resource": "uploads", "permission": "doc:write" }
] }
```

`POST /authz/v1/storage/sign` with `{"bucket": "uploads", "key": "org/<org id>/reports/q3.pdf", "operation": "read"}`
returns a URL valid for 60 seconds (`GET`, or `PUT` for `write`). Keys must start
with `org/<org id>/`; the bucket name is the policy resource.

**Realtime.** Create the endpoint through the Auth app; clients connect with
their Auth access token:

```sh
gregale realtime create shop --auth shop-auth --allowed-origin https://shop.example.com
```

Name channels `org:<org id>:<kind>:<name>`, for example `org:…:room:lobby`, and
add a policy such as `{"action": "realtime:read", "resource": "room", "permission": "org:read"}`.
Subscriptions are checked when they start. A ban, deletion or global sign-out
stops new subscriptions; an open connection keeps its grant until it closes.
Do not set connect, message or disconnect callback paths on this endpoint; its
callback URL is the Auth app.

**Workflows.** Let users start workflows they are allowed to:

```sh
gregale auth create shop-auth --database shop-db --site-url https://shop.example.com --resume --workflows
```

This mints a key that can only publish events and seals it in the Auth app.
`POST /authz/v1/workflows/{name}/start` with `{"org": "<org id>", "input": {...}, "idempotency_key": "req-1"}`
checks `workflow:start` on `org/<org id>/workflow/<name>` and publishes an
`auth.workflow.start` event with `data.workflow`, `data.org_id`, `data.user_id`
and `data.input`. Subscribe your workflow to that event type from the Auth
app's source (`app.<auth app id>`). Repeating a start with the same
`idempotency_key` returns the original receipt.

## Manage users and import them

Create an admin key, then open **Manage application Auth users** on the app's
dashboard page and paste it:

```sh
gregale auth admin-key create shop-auth
```

The key is shown once; the Auth app stores only its SHA-256. The dashboard page
sends the key only to your Auth app and keeps it in the browser tab until you
close it or choose **Forget key**. User records go from your Auth app to your
browser and never through Gregale. From the page you can search users, see their
organizations and active sessions, sign a user out everywhere, ban or unban
them (a ban also ends their sessions), and delete them, and browse
organizations with their members and pending invitations. Deleting the last
owner of an organization that has other members is refused; organizations
whose only member was the deleted user are removed with them.

`gregale auth admin-key list shop-auth` shows key IDs and
`gregale auth admin-key revoke shop-auth ID` removes one. Both restart the Auth
app so running instances load the change.

To move users from another system, write one JSON object per line (or a JSON
array):

```json
{"id": "3c4d5e6f-7a8b-4c9d-8e0f-1a2b3c4d5e6f", "email": "ada@example.com", "password_hash": "$2b$10$…", "email_confirmed": true, "user_metadata": {"name": "Ada"}}
```

```sh
gregale auth import shop-auth --file users.jsonl --dry-run
printf '%s' "$ADMIN_KEY" | gregale auth import shop-auth --file users.jsonl --admin-key-stdin
```

`password_hash` accepts bcrypt (`$2a$`, `$2b$`, `$2y$`), argon2 (`$argon2i`,
`$argon2id`) and Firebase scrypt (`$fbscrypt$`) hashes, so users keep their
passwords; users without one sign in with a magic link, OAuth or a password
reset. Keeping `id` keeps foreign keys in your tables valid. Plain-text
passwords are refused. Re-running a file is safe: users that already exist are
reported as `email_exists`.

## Current limits

- The Auth app counts as an ordinary deployed app for plan quotas and RAM
  billing.
- An Auth app holds at most 4 signing keys.
- The dashboard page reaches the Auth app at its canonical address. If you
  serve the dashboard from another origin than the Gregale API URL that
  `gregale auth create` used, the Auth app refuses the page's requests.
