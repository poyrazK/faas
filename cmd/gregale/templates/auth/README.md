# Application Auth

This template runs a pinned GoTrue (Supabase Auth) v2.197.0 server for your
application's own users (ADR-1190).
It is not your Gregale account login. Create and configure it with
`gregale auth create NAME --database DATABASE --site-url URL`.

The public API is under `/auth/v1`: sign-up, password and magic-link sign-in,
token refresh, sign-out, MFA and the user endpoint. Access tokens are ES256
JWTs with `sub`, `session_id`, `aud=authenticated` and a 10-minute lifetime.
Verify them with the JWKS at `/auth/v1/.well-known/jwks.json`, or let Gregale
resolve it: `gregale data-api create NAME --database DATABASE --auth AUTH_APP`
or `gregale edge rule create ... --kind jwt --jwt-auth AUTH_APP`.

Users live in the `auth` schema of your own database. The `auth` binding acts
as the owner of only the `auth` and `gregale_auth` schemas; it cannot read or
change your other tables. GoTrue migrations run in the release task, never at
serving startup. The image verifies every pinned upstream migration byte for
byte. The release runner applies the verified files directly, with an overlay
for the file whose grants assumed a role named `postgres`. GoTrue's stock
`migrate` command uses embedded SQL and does not read that overlay; the template
does not invoke it. Existing versions stay in `auth.schema_migrations`, and
each migration/version record commits together. Raw files, manifest and overlay
remain in the image for release-time verification.

Organizations, memberships, roles and invitations live in the `gregale_auth`
schema. The `/authz/v1` API manages them and answers `authorize` checks;
`gregale_auth.authorize(action, 'org/<id>/<kind>/<name>')` and
`gregale_auth.has_permission(org, permission)` serve row-level security. The
release task applies `sql/` after GoTrue's migrations, then the declarative
`AUTH_AUTHZ` roles and policies.

The internal Product Control team API requires
`AUTH_PRODUCT_CONTROL_TEAM_ENABLED=1`; it is off by default. After applying
`sql/0010_reviewed_team_actions.sql` through the release task, it provides
`GET /authz/v1/orgs/{org}/team` and reviewed `/team-actions` create/read/history
routes. They require a live session with `org:read` and `members:manage`.
Membership changes and their retry/audit receipts commit in the same transaction.
Invitations reuse the existing hosted acceptance page and email provider. Manual
invitation links are returned once, never retained in receipts or returned by
retries. Product Control's separate apid flag and opt-in product permission are
also required for its portal. The existing Auth APIs keep their own permissions.

`POST /authz/v1/storage/sign` signs 60-second URLs for buckets bound to this
app (`GREGALE_S3_<NAME>_*`); `POST /realtime/authorize-channel` serves managed
realtime endpoints created with `gregale realtime create APP --auth`; and
`POST /authz/v1/workflows/{name}/start` publishes an `auth.workflow.start`
event with the `events:publish` key from `gregale auth create --workflows`.

Realtime callbacks must include the verified token's UUID `session_id` and user
ID. The callback scopes `gregale_auth.authorize` to that exact session, so a
signed-out device cannot borrow another device's session. Deploy a daemon with
the verified session handoff before updating an existing Auth app, then reconnect
clients. No SQL migration is needed for this change.

The create command seals these values: `AUTH_EXTERNAL_URL`, `AUTH_SITE_URL`,
`AUTH_REDIRECT_URLS`, `AUTH_AUTOCONFIRM`, `AUTH_DISABLE_SIGNUP`, `AUTH_AUTHZ`,
`AUTH_JWT_SECRET`, an ES256 key as `AUTH_JWT_KEY_<ID>` and `AUTH_JWT_SIGNER_<ID>`.
Each signing key is its own secret; the newest `AUTH_JWT_SIGNER_<ID>` (its
value is the Unix time it was activated) names the key that signs, and the
others only verify. `gregale auth keys` rotates them. Configure sign-in
providers, MFA and password rules with `gregale auth config` and
`gregale auth providers`, which write `GOTRUE_EXTERNAL_*`, `GOTRUE_MFA_*`,
`GOTRUE_PASSWORD_*`, `GOTRUE_PASSKEY_ENABLED` and `GOTRUE_SECURITY_CAPTCHA_*`
secrets, and the hosted pages' `AUTH_UI_*` branding, `AUTH_CAPTCHA_SITE_KEY`,
`AUTH_PASSKEY_RP_ID` and `AUTH_AUDIT_RETENTION_DAYS`. The front derives
GoTrue's WebAuthn relying party from the Auth app's host. Any other `GOTRUE_*` secret stops startup because Gregale owns that
setting. `GET /settings/v1` reports the running configuration without
credentials.

Email goes through GoTrue's send-email hook to a loopback-only listener in the
front, which delivers through Resend or Postmark over HTTPS (`AUTH_EMAIL_*`);
tenant egress blocks SMTP. Hosted pages are served under `/ui`. The front drops
GoTrue's info-level logs and redacts email addresses from the rest.

With `AUTH_OAUTH_SERVER=1` (`gregale auth config NAME --oauth-server on`) GoTrue
is also an OAuth 2.1 authorization server for third-party apps and MCP
clients: issuer `<host>/auth/v1`, metadata at
`/.well-known/oauth-authorization-server/auth/v1`, consent at `/ui/consent`.
`AUTH_OAUTH_REGISTRATION=1` lets clients register themselves; owners manage
clients with `gregale auth oauth-clients`. A client's access token carries
`client_id`, `scope` and, as its audience, the RFC 8707 resource it asked for
(or `oauth-client`), so verifiers of `aud=authenticated` refuse it. On this
host it reaches only `/auth/v1/oauth/userinfo`, `POST /auth/v1/logout?scope=local`
and `POST /authz/v1/authorize`.

`gregale auth config NAME --anonymous on` lets people start without an
account (`signInAnonymously`); their tokens carry `is_anonymous`, they cannot
create organizations, and `--anonymous-retention DAYS` deletes those idle
that long (`AUTH_ANONYMOUS_RETENTION_DAYS`). `--identity-linking on`
(`AUTH_IDENTITY_LINKING`) lets users link a provider account, which also
makes an anonymous user permanent, and unlink sign-in methods.

Run the portable tests with `npm ci` and `npm test`. To include the reviewed
team transaction tests, set `GREGALE_AUTH_TEAM_TEST_DATABASE_URL` to a disposable
local database named `product_control_team_auth_<suffix>`. That test resets its
Auth schemas and applies the Gregale migrations twice. It supplies a minimal
GoTrue user/session schema for SQL contract tests; actual GoTrue session,
configured-provider email and hosted acceptance qualification still require the
live Product Control pilot.
