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
generates one ES256 signing key and seals it with the other settings, and
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
pinned upstream migration byte for byte and replaces one migration whose grants
assumed a role named `postgres`.

## Configure providers and policies

Set these app secrets to configure GoTrue, then redeploy:

- `GOTRUE_EXTERNAL_*` for OAuth providers such as GitHub or Google
- `GOTRUE_MFA_*` for MFA factors
- `GOTRUE_PASSWORD_*` for password rules
- `GOTRUE_SECURITY_CAPTCHA_*` for CAPTCHA

Any other `GOTRUE_*` secret stops startup, because Gregale owns that setting.

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

## Current limits

- Storage downloads, realtime channels and workflow starts call
  `authorize()` in a later phase; today, call `POST /authz/v1/authorize` from
  your backend.
- The Auth app counts as an ordinary deployed app for plan quotas and RAM
  billing.
