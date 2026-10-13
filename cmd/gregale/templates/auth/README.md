# Application Auth

This template runs a pinned GoTrue (Supabase Auth) v2.197.0 server for your
application's own users ([ADR-1190](https://gregale.dev/docs/application-auth)).
It is not your Gregale account login. Create and configure it with
`gregale auth create NAME --database DATABASE --site-url URL`.

The public API is under `/auth/v1`: sign-up, password and magic-link sign-in,
token refresh, sign-out, MFA and the user endpoint. Access tokens are ES256
JWTs with `sub`, `session_id`, `aud=authenticated` and a 10-minute lifetime.
Verify them with the JWKS at `/auth/v1/.well-known/jwks.json`, for example in a
Data API: `gregale data-api create NAME --database DATABASE --issuer
https://<auth-host>/auth/v1 --jwks-url https://<auth-host>/auth/v1/.well-known/jwks.json
--audience authenticated`.

Users live in the `auth` schema of your own database. The `auth` binding acts
as the owner of only the `auth` and `gregale_auth` schemas; it cannot read or
change your other tables. GoTrue migrations run in the release task, never at
serving startup. The image verifies every pinned upstream migration byte for
byte and replaces one file whose grants assumed a role named `postgres`.

The create command seals these values: `AUTH_EXTERNAL_URL`, `AUTH_SITE_URL`,
`AUTH_REDIRECT_URLS`, `AUTH_AUTOCONFIRM`, `AUTH_DISABLE_SIGNUP`,
`AUTH_JWT_KEYS` (one ES256 signing key, generated once) and `AUTH_JWT_SECRET`.
Configure sign-in providers, MFA, password rules and CAPTCHA by setting
`GOTRUE_EXTERNAL_*`, `GOTRUE_MFA_*`, `GOTRUE_PASSWORD_*` or
`GOTRUE_SECURITY_CAPTCHA_*` secrets; any other `GOTRUE_*` secret stops startup
because Gregale owns that setting.

Email delivery is not available yet: tenant egress blocks SMTP and the HTTPS
email hook arrives in a later phase. Until then, use `--autoconfirm` for
development or OAuth providers. GoTrue logs at `warn` so verification tokens in
request URLs are not written to logs.
