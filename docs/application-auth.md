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

- **Data API:** `gregale data-api create NAME --database DB --issuer
  https://<auth-host>/auth/v1 --jwks-url
  https://<auth-host>/auth/v1/.well-known/jwks.json --audience authenticated`,
  then write row-level security policies on `sub`.
- **Edge JWT rules:** use the same issuer, JWKS URL and audience.

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

## Current limits

- Email delivery is not available yet. Tenant egress blocks SMTP, and the HTTPS
  email hook is a later phase. Use OAuth providers, or `--autoconfirm` for
  development.
- Organizations, invitations, roles and the shared `authorize()` policy
  function are a later phase, as is `--auth NAME` wiring for the Data API and
  edge JWT rules.
- The Auth app counts as an ordinary deployed app for plan quotas and RAM
  billing.
