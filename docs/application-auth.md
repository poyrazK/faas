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
- **Your own backend:** the Go and Python SDKs (see below).

Sign-out and bans end refresh immediately. A token already issued stays valid
for edge-only checks until it expires, at most 600 seconds later.

### In your backend

The Go and Python SDKs check a token's ES256 signature against the Auth app's
published keys, its issuer (`https://<auth-host>/auth/v1`), its audience
(`authenticated`) and its expiry, allowing 60 seconds of clock skew. They cache
the keys for five minutes and refetch at most every 30 seconds when a token
names an unknown key, so key rotation needs no restart and a parked Auth app is
not woken by verification. Like edge rules, a verified token stays valid until
it expires; `Authorize` asks the Auth app, which also checks that the session
is still active.

Go (`github.com/poyrazK/faas/sdk/go/auth`, standard library only):

```go
client, err := auth.New("https://shop-auth.gregale.app")
if err != nil {
	return err
}
// 401 without a valid token, 503 when the keys cannot be fetched.
mux.Handle("/api/docs/", client.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.FromContext(r.Context()) // claims.Subject is the user ID
	token, _ := auth.BearerToken(r)
	ok, err := client.Authorize(r.Context(), token, "read", auth.Resource(orgID, "doc", docID))
	// ...
})))
```

Python (`pip install 'faas_sdk[auth]'`), here as a FastAPI dependency:

```python
from fastapi import Header, HTTPException
from faas_sdk.auth import AuthClient, AuthUnavailableError, InvalidTokenError, NoTokenError, bearer_token, resource

auth = AuthClient("https://shop-auth.gregale.app")

async def current_user(authorization: str | None = Header(None)):
    try:
        return await auth.verify_async(bearer_token(authorization))
    except (NoTokenError, InvalidTokenError):
        raise HTTPException(401)
    except AuthUnavailableError:
        raise HTTPException(503)

# allowed = await auth.authorize_async(token, "read", resource(org_id, "doc", doc_id))
```

`verify` and `authorize` are the synchronous forms for Flask or Django.

## Your data

Users, sessions and identities live in the `auth` schema of your own database,
so a `pg_dump` exports them. The `auth` binding acts as the owner of only the
`auth` and `gregale_auth` schemas: it cannot read or change your other tables,
and your runtime and read-only bindings cannot read `auth`. Creation fails if
your database already has an `auth` schema owned by another role.

GoTrue migrations run in the deployment's release task. The image checks every
pinned upstream migration byte for byte and replaces one migration whose grants
assumed a role named `postgres`.

## Change settings

`gregale auth config shop-auth` shows the running app's settings. Add flags to
change them; the app restarts to apply the change. The dashboard's Auth page
(`/dashboard/apps/shop-auth/auth`) shows and edits the same settings,
providers and email delivery, and rotates signing keys.

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
| `--events on\|off` | Publish user lifecycle events to your event bus (see Lifecycle events). |
| `--passkeys on\|off`, `--passkey-rp-id DOMAIN` | Passkey sign-in (see Passkeys). |
| `--brand-name`, `--brand-color`, `--brand-logo-url` | The hosted pages' name, accent colour and logo (see Hosted pages); `none` removes one. |
| `--captcha turnstile\|hcaptcha\|off`, `--captcha-site-key`, `--captcha-secret-stdin` | A CAPTCHA on sign-up, sign-in and password reset (see CAPTCHA). |
| `--activity-retention DAYS\|forever` | How long sign-in activity is kept (see Manage users). |
| `--sms PROVIDER\|off`, `--sms-account`, `--sms-sender`, `--sms-secret-stdin` | Phone sign-in by text message (see Phone sign-in). |
| `--sso on\|off` | Organization single sign-on with SAML (see Organization single sign-on). |

Settings are app secrets, which are write-only, so the CLI and the dashboard
read them back from the running app's public `/settings/v1`. It reports only
values that are public anyway or harmless: URLs, enabled providers, password
rules, whether MFA, passkeys and CAPTCHA are on, the email provider and
sender (never its API key), the branding, the activity retention and the
signing key IDs.

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

### Passkeys

```sh
gregale auth config shop-auth --passkeys on
```

Users sign in with a fingerprint, face or device PIN instead of a password. The
hosted sign-in page gains **Sign in with a passkey**, and after a password
sign-in or sign-up it offers to add one to users who have none. Passkeys are
discoverable, so users don't type an email address. In your own pages, use
`auth.signInWithPasskey()` and `auth.passkeys.register()`, `list()`, `rename()`
and `remove()` from `@gregale/auth`.

A passkey belongs to one domain. By default that is the Auth app's host, so
passkeys work on the hosted pages. If the Auth app is on a custom domain under
your site's domain (`auth.example.com` for `shop.example.com`), share the parent
domain so both use the same passkeys:

```sh
gregale auth config shop-auth --passkey-rp-id example.com
```

Only HTTPS origins on that domain may use them: the Auth app, your site URL and
your redirect URLs that are on it.

### Phone sign-in

Users can sign in with a code texted to their phone, sent through your own SMS
account; Gregale sells no messages. Supported: `twilio` (Programmable
Messaging), `twilio_verify`, `messagebird`, `textlocal` and `vonage`.

```sh
printf '%s' "$TWILIO_AUTH_TOKEN" | gregale auth config shop-auth --sms twilio --sms-account "$TWILIO_ACCOUNT_SID" --sms-sender "$TWILIO_MESSAGING_SERVICE_SID" --sms-secret-stdin
```

| Provider | `--sms-account` | `--sms-sender` | `--sms-secret-stdin` |
| --- | --- | --- | --- |
| `twilio` | Account SID | Messaging service SID or phone number | Auth token |
| `twilio_verify` | Account SID | Verify service SID | Auth token |
| `messagebird` | — | Originator | Access key |
| `textlocal` | — | Sender | API key |
| `vonage` | API key | From number or name | API secret |

The hosted sign-in page gains **Sign in with a phone number**
(`/ui/phone`): it texts a code, a new number signs up unless sign-up is
closed, and the code signs the user in. The message reads "Your <name> code is
123456" with the hosted pages' display name; set `GOTRUE_SMS_TEMPLATE` as an
app secret to change it. In your own pages, use
`auth.signInWithPhone({ phone })` and `auth.verifyPhone({ phone, code })` from
`@gregale/auth`. Numbers need their country code (`+447700900123`).

Anyone can make the Auth app text any number, which costs you money and
attracts SMS pumping fraud. Turn on CAPTCHA with phone sign-in: it protects
sending a code. GoTrue also sends at most 30 messages an hour per running
instance and one code a minute per number. Codes last five minutes; set
`GOTRUE_SMS_OTP_EXP` (seconds) to change that. `--sms off` turns phone sign-in off and
removes the provider's credentials; changing provider removes the previous
one's.

### CAPTCHA

```sh
printf '%s' "$TURNSTILE_SECRET" | gregale auth config shop-auth --captcha turnstile --captcha-site-key "$TURNSTILE_SITE_KEY" --captcha-secret-stdin
```

GoTrue then requires a CAPTCHA token on sign-up, password, magic-link and
phone sign-in, password reset and passkey sign-in. The hosted pages render the
provider's widget (Cloudflare Turnstile or hCaptcha) and allow its origin, and
only its origin, in their content security policy. In your own pages, pass the
widget's token as `captchaToken` to `signUp`, `signInWithPassword`,
`signInWithMagicLink`, `resetPasswordForEmail` and `signInWithPasskey`.
`--captcha off` removes the keys. Changing CAPTCHA always takes all three
values.

### Other GoTrue settings

Other `GOTRUE_EXTERNAL_*`, `GOTRUE_MFA_*`, `GOTRUE_PASSWORD_*`,
`GOTRUE_PASSKEY_*` and `GOTRUE_SECURITY_CAPTCHA_*` settings can still be set as
app secrets. Any other `GOTRUE_*` secret stops startup, because Gregale owns
that setting.

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

The dashboard's Auth page lists the keys with their state. **Add a standby
key**, wait until it shows as verifying (the page follows the restart), choose
**Make it sign**, then **Revoke** the previous key ten minutes later. The page
offers each step only when the running app is ready for it.

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

## Organization single sign-on

Let your business customers sign in through their company's identity
provider (Okta, Microsoft Entra ID, Google Workspace or any SAML 2.0 provider)
and join their organization automatically:

```sh
gregale auth config shop-auth --sso on
printf '%s' "$ADMIN_KEY" | gregale auth sso add shop-auth --metadata-url https://acme.okta.com/app/abc/sso/saml/metadata --domains acme.com --org acme --role member --admin-key-stdin
```

`--sso on` creates the app's SAML signing key once and keeps it, even when
SSO is turned off, so identity providers keep trusting the app. `sso add`
prints what to register with the identity provider: the entity ID
(`https://<auth-host>/auth/v1/sso/saml/metadata`, which also serves the
metadata) and the ACS URL (`https://<auth-host>/auth/v1/sso/saml/acs`). Use
`--metadata-file` for metadata XML instead of a URL.

Users whose email address is in one of `--domains` sign in at
`/ui/sso` (the hosted sign-in page links to it) or with
`auth.signInWithSSO({ domain })` from `@gregale/auth`, which returns the
identity provider's URL to navigate to. With `--org`, they join that
organization with `--role` (default `member`, never `owner`) the first time
they sign in, and the provider's existing users join when you link it.
`gregale auth sso list`, `update` (`--org none` unlinks; members stay) and
`remove` manage providers. Identity providers live in your database, like
your users.

An SSO user is a separate account from a password or OAuth account with the
same address. Because an identity provider vouches only for its own domains,
an SSO user can accept an organization invitation only for an address in one
of its provider's domains.

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
pages handle sign-in (password, magic link, enabled OAuth providers, passkeys,
phone and organization SSO when they are on), sign-up,
password reset and invitations, then return to `redirect_to` with the session
in the URL fragment. Only your site URL and paths under `--redirect-urls` are
accepted as targets.

Brand them with your name, accent colour and logo:

```sh
gregale auth config shop-auth --brand-name Shop --brand-color '#4f46e5' --brand-logo-url https://cdn.example.com/logo.svg
```

The name also appears in emails and passkey prompts. The logo loads from its
HTTPS URL; the pages allow that URL's origin for images and nothing else.

Read the session with `@gregale/auth`:

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
Retained WebSocket and SSE channel grants have a 30-second lifetime. A ban,
deletion, global sign-out or membership change is enforced when that check next
runs. Expired grants block further channel output until the check succeeds.
Idle WebSocket cleanup also includes the history poll interval and callback
deadline. A definitive denial removes the subscription; a callback outage ends
the connection so the client can reconnect. Already delivered data cannot be
recalled. Active subscriptions can keep the Auth app awake through these checks.

The callback checks the token's specific `session_id`, its user and the channel
policy. Signing out one session revokes its retained channels on their next
refresh even if another device remains signed in. Retained WebSockets and SSE
also reverify their JWTs; tokens must include `exp`, and clients reconnect with a
refreshed token after expiry. Signing-key changes follow the existing JWKS cache
contract. Principal access controls remain available for explicit revocation.
Legacy management subscriptions, inbox/direct-message session authorization and
subscription tickets keep their existing access-control contracts.

Existing Auth apps need a redeploy from the updated template for session-specific
checks. Deploy the realtime daemon first, update the Auth app, then reconnect
clients; the updated Auth callback rejects callers that omit the session ID.
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

## Lifecycle events

Turn on lifecycle events to react to users in a workflow or event worker, for
example to send a welcome email after confirmation or to clean up after a
deletion:

```sh
gregale auth config shop-auth --events on
```

The Auth app then publishes these events from its source (`app.<auth app id>`):

| Type | When | `data` besides `user_id` |
| --- | --- | --- |
| `auth.user.created` | A user signs up, is invited, imported or created | `provider`, `anonymous` |
| `auth.user.confirmed` | A user confirms their email address or phone number | `channel` (`email` or `phone`) |
| `auth.user.signed_in` | A new session starts (password, magic link, OAuth, sign-up) | `session_id`, `aal` |
| `auth.user.banned` | A ban starts | `until` |
| `auth.user.unbanned` | A ban is lifted | |
| `auth.user.deleted` | A user is deleted | |

Events carry the user ID and these facts only, never an address, a phone number or metadata,
because Gregale stores published events. Look the user up in your own
`auth.users` table when you need more. Triggers record each change in the same
transaction, and the Auth app publishes after the request that caused it, so
publishing never wakes a parked app or keeps your database awake. Delivery is
at least once: each event's key is unique, and failures are retried with
backoff for up to three days. Events count towards your plan's event limits
like any other. `--events on` mints the same `events:publish`-only key that
`--workflows` uses, if the app has none. `--events off` stops recording and
drops undelivered events.

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

The **Activity** tab lists GoTrue's audit log newest first: sign-ins,
sign-ups, password resets and changes, and administrator actions such as bans
and deletions, which are reported against the affected user. A user's panel
shows their ten most recent entries. Search for an event such as `login`, or
part of an email address. From a terminal:

```sh
printf '%s' "$ADMIN_KEY" | gregale auth activity shop-auth --search login --admin-key-stdin
```

GoTrue v2.197.0 does not record client IP addresses in this log. **Export CSV**
on the Activity tab downloads up to 10,000 entries matching the search; the file
is built in your browser from the Auth app's responses.

GoTrue never prunes this log, so it grows in your database. Keep it for a
number of days instead:

```sh
gregale auth config shop-auth --activity-retention 90
```

The Auth app deletes older entries in batches after requests, at most every six
hours, so pruning never wakes it or your database.

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
