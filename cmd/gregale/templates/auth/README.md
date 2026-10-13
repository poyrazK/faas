# Application Auth

This template runs a pinned GoTrue (Supabase Auth) v2.197.0 server for your
application's own users ([ADR-1190](https://gregale.dev/docs/application-auth)).
It is not your Gregale account login. Create and configure it with
`gregale auth create NAME --database DATABASE --site-url URL`.

The public API is under `/auth/v1`: sign-up, password and magic-link sign-in,
token refresh, sign-out, MFA and the user endpoint. Access tokens are ES256
JWTs with `sub`, `session_id`, `aud=authenticated` and a 10-minute lifetime.
Verify them with the JWKS at `/auth/v1/.well-known/jwks.json`, or let Gregale
resolve it: `gregale data-api create NAME --database DATABASE --auth AUTH_APP`
or `gregale edge-rules create ... --kind jwt --jwt-auth AUTH_APP`.

Users live in the `auth` schema of your own database. The `auth` binding acts
as the owner of only the `auth` and `gregale_auth` schemas; it cannot read or
change your other tables. GoTrue migrations run in the release task, never at
serving startup. The image verifies every pinned upstream migration byte for
byte and replaces one file whose grants assumed a role named `postgres`.

Organizations, memberships, roles and invitations live in the `gregale_auth`
schema. The `/authz/v1` API manages them and answers `authorize` checks;
`gregale_auth.authorize(action, 'org/<id>/<kind>/<name>')` and
`gregale_auth.has_permission(org, permission)` serve row-level security. The
release task applies `sql/` after GoTrue's migrations, then the declarative
`AUTH_AUTHZ` roles and policies.

The create command seals these values: `AUTH_EXTERNAL_URL`, `AUTH_SITE_URL`,
`AUTH_REDIRECT_URLS`, `AUTH_AUTOCONFIRM`, `AUTH_DISABLE_SIGNUP`, `AUTH_AUTHZ`,
`AUTH_JWT_KEYS` (one ES256 signing key, generated once) and `AUTH_JWT_SECRET`.
Configure sign-in providers, MFA, password rules and CAPTCHA by setting
`GOTRUE_EXTERNAL_*`, `GOTRUE_MFA_*`, `GOTRUE_PASSWORD_*` or
`GOTRUE_SECURITY_CAPTCHA_*` secrets; any other `GOTRUE_*` secret stops startup
because Gregale owns that setting.

Email goes through GoTrue's send-email hook to a loopback-only listener in the
front, which delivers through Resend or Postmark over HTTPS (`AUTH_EMAIL_*`);
tenant egress blocks SMTP. Hosted pages are served under `/ui`. The front drops
GoTrue's info-level logs and redacts email addresses from the rest.
