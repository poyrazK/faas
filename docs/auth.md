# Authentication

Log in locally with the CLI; it stores a short-lived session in the operating-system keychain.

```bash
gregale login
gregale whoami
gregale logout
```

Browser-approved logins mint a 30-day CLI key tied to this device. The CLI
stores that key's non-secret id and `gregale logout` revokes exactly that key
before clearing the local keychain. Logging in again, or removing a connection
profile, revokes the CLI key of the login it replaces. Use `gregale key list`
(its ID column) and `gregale key delete <id>` to remove a session from a lost
device; CLI sessions are labelled `cli-login` and never expose their plaintext
after creation.

For CI, create a scoped project or organization token and expose it as `GREGALE_TOKEN`, or pass it once with `gregale login --token "$GREGALE_TOKEN"`. Prefer the narrowest scope and a short expiration. Do not commit tokens or pass them as command-line arguments where process listings can expose them.
Tokens supplied through `GREGALE_TOKEN` or `gregale login --token` remain
non-owning: logout clears local state but does not revoke a shared CI key.
`GREGALE_TOKEN` outranks a saved login, so `gregale login` and `gregale start`
refuse to run while it is set (unless `--token` passes the same value), and
`gregale logout` says when `GREGALE_TOKEN` is still active.
`FAAS_TOKEN`, the variable's earlier name, still works; when both are set,
`GREGALE_TOKEN` wins. The Terraform provider reads `GREGALE_TOKEN` too.

For a Runs-only agent, mint an account key with `runs:write` instead of the
default full-access `admin` key:

```sh
gregale key create agent-runner --scopes runs:write
```

`runs:read` can inspect the Runs capability contract, receipts, and event
streams. `runs:write` can submit and cancel runs and includes those read
permissions so an agent can follow its work. Existing `apps:read` keys retain
Runs read access, and existing `deploy:write` keys retain submit/cancel access.
Both scopes remain account-wide; they do not bind a key to an individual run.

For an agent that uses Gregale's hosted MCP server without a browser, such as
one in CI, mint an agent key:

```sh
gregale key create ci-agent --scopes agent,deploy:write
```

Only keys with the `agent` scope open the hosted MCP server; other keys are
refused there. An agent key always includes `apps:read`, can never include
`admin`, and expires after 90 days unless you choose otherwise. Its other
scopes pick the agent's tools; see
[headless agents](mcp.md#headless-agents-with-an-agent-key).
`gregale mcp connection list` shows agent keys beside hosted MCP sign-ins, and
`gregale mcp connection revoke` revokes one or all of them; see
[connected agents](mcp.md#see-and-revoke-connected-agents).

Interactive users can enable MFA from the account settings page. Each authenticator code works once: a code that already verified a session is refused if it is sent again, so wait for the next code. A `401` means the session or token is missing/expired; a `403` means the identity is valid but lacks the required project or organization scope. Rotate a compromised token immediately and review the audit log.

`gregale account mfa enroll`, `confirm`, `verify`, `recover`, and `disable`
open the corresponding form in your signed-in browser. You can use recovery
before completing CLI login. Enter passwords and codes in the browser;
the CLI prints a URL, and `--json` returns that URL with `requires_browser: true`.
In a terminal without a browser, open the printed URL on a device where you
can sign in. Setup shows the QR code and recovery codes in the browser.
Legacy code, password, and `--qr-out` arguments remain accepted but do not
submit credentials or write setup material. A browser handoff is complete
only after the browser confirms the action succeeded.

Enrollment confirmation spends its authenticator code and grants the current
session a fresh MFA verification. Accounts with a password must enter their
current password to confirm setup. A pending setup cannot verify a session
or provide recovery access. An already-enrolled account uses `verify`
for further challenges; repeating `confirm` returns a conflict. A suspended
account can enroll, verify, recover or reset its authenticator so billing
recovery never depends on first restoring billing.

MFA is optional for ordinary accounts. Plan changes, billing details and key
creation ask for recent verification only when MFA is enabled or explicitly
required. Enrollment, an authenticator code and a saved recovery code each
establish five minutes of recent verification; enrollment needs no second code.
Resetting MFA also clears the browser's pending challenge unless an explicit
policy or operator access requires a replacement.

If your last recovery code remains, use it to reset the authenticator and set
up a replacement. If neither the device nor codes are available, choose email
recovery. Its link can be confirmed after the existing 24-hour waiting period.
If you have lost the device, the codes and the mailbox, contact
support@gregale.dev. Support checks two independent proofs of ownership (such
as payment details and a sign-in with your password or linked provider), mails
the account's address and any co-owners, and after 72 hours without objection
turns two-factor authentication off; you then sign in, set it up again and,
if needed, change your email. Support never changes the sign-in email for you.
To replace your recovery codes while you still have your authenticator, run
`gregale account mfa rotate` (or use **New recovery codes** on the
two-factor page) after verifying the session; the previous codes stop working
and you get an email.

At the dashboard sign-in check you can tick **Remember this browser for 30
days**: that browser then skips the code at sign-in (not for sensitive
actions) until it expires or you forget it on the account page. Operator
accounts are always asked. See [account](account.md#profile-sign-in-and-sessions).

## API keys

An API key lets a script, CI job or agent act for your account. Create one
with the narrowest scope that does the job; the key is shown once:

```bash
gregale key create ci-deploy --scopes deploy:write
gregale key list                 # ID, label, status, created, expires
gregale key delete KEY_ID        # revoke it now
```

Without `--scopes` the key gets `admin`, which can do everything your account
can. Common scopes:

| Scope | Allows |
|---|---|
| `admin` | Everything, including billing, key management and deleting the account. |
| `apps:read` | Read apps, deployments, logs, instances, crons, domains and audit events. |
| `deploy:write` | Create, change and delete apps, deploy, roll back, invoke, wake and park. |
| `secrets:read` / `secrets:write` | Read or change secrets. |
| `env:read` / `env:write` | Read or change configuration variables. |
| `usage:read` | Read usage and cost. |
| `runs:read` / `runs:write` | Inspect, or submit and cancel, disposable runs. |
| `agent` | Open the hosted MCP server; see [headless agents](mcp.md#headless-agents-with-an-agent-key). |

Scopes limit what a key can do through the Gregale API, not traffic to your
apps. An app that requires an API key (`gregale app APP --require-authn`)
accepts any active key of the owning account, whatever its scopes: an
`apps:read` or `agent` key can send `GET` and `POST` requests to it like an
`admin` key. Keys of other accounts get `403`. Give keys that must not reach
your apps' data to a separate account, and authenticate the callers inside
the app ([application auth](application-auth.md)) when only some of them
should get through.

The number of keys an account can hold depends on the plan: 3 on Free, 10 on
Hobby, 50 on Pro and 200 on Scale. Keys still inside a rotation grace window
count. Past the limit, creating a key fails with `409 api_key_limit_exceeded`.
For keys an organization owns, see
[organization API keys](account.md#organization-api-keys).

Your code never sees a Gregale API key: once a private app's key requirement
or `bearer` mode has verified one, Gregale strips the `Authorization` header,
since the key belongs to the caller's account. A `basic` password is passed
on, as is any `Authorization` header no gate reads.

### Expiry and rotation

Every new key expires one year after it is made. Agent keys expire after 90
days, and the keys a browser-approved `gregale login` makes after 30 days. The
EXPIRES column of `gregale key list` shows when. A request with an expired key
fails with `401 api_key_expired`, and a revoked key with `401 api_key_revoked`.

Rotate a key before it expires. Rotation returns a new key and keeps the old
one working for the account's grace window, 7 days unless you change it, so
you can update every place that uses it:

```bash
gregale key rotate KEY_ID                  # old key works for the grace window
gregale key rotate KEY_ID --revoke-old     # the key leaked: old key stops now
gregale key grace-window set --days 3      # 0 to 30 days, for future rotations
```

Creating or rotating a key emails you, so a key you did not make is noticed.

## App access control

By default an app's public URL answers anyone; on some plans a new app starts
private (see [Test a private app](quickstart.md#test-a-private-app)).
`--public-auth` picks who gets through:

```bash
gregale app update my-api --public-auth bearer
gregale app update my-api --public-auth basic --basic-user team --basic-pass "$PASS"
gregale app update my-api --public-auth ip_allowlist --ip-allowlist 203.0.113.0/24 --ip-allowlist 198.51.100.7/32
gregale app update my-api --public-auth open
```

| Mode | Who gets through | Plans |
|---|---|---|
| `open` | Anyone. | All |
| `bearer` | Requests with `Authorization: Bearer KEY`, where KEY is an API key of the app's account with `apps:read` or `admin`. A key from another account gets `403`. | Hobby, Pro, Scale |
| `basic` | Requests with HTTP basic auth matching the username and password you set. | Pro, Scale |
| `ip_allowlist` | Requests from the CIDR ranges you list. Others get `403`. | Pro, Scale |
| `internal_only` | Only Gregale itself, for example to run the app's crons. | All |

Refused requests never reach your app. Each mode replaces the previous one,
and `--public-auth open` removes it. On a plan without the mode, the change
fails with `403 plan_public_auth_bearer_not_allowed`,
`plan_public_auth_basic_not_allowed` or
`plan_public_auth_ip_allowlist_not_allowed`.

- **Basic auth:** the username is 1 to 128 bytes and the password 12 to 256
  bytes. Gregale stores them encrypted. After 10 wrong passwords in a minute
  from one network (an IPv4 /24 or IPv6 /64), that network gets `429` until
  the minute is over.
- **IP allowlist:** each entry is a CIDR such as `203.0.113.0/24`; a single
  address is `/32` (IPv4) or `/128` (IPv6). A `/0` range is refused. Pro apps
  can list 16 ranges and Scale apps 64; more fails with
  `400 public_auth_ip_allowlist_too_long`. The check uses the caller's
  address as Gregale's edge sees it, so list your office or VPN egress
  addresses, not private ones.

`gregale app update my-api --no-require-authn` drops the API key requirement a
private app starts with and opens a `bearer` URL. It leaves `basic`,
`ip_allowlist` and `internal_only` in place. To block or rate-limit callers
by path, header or country while staying public, use
[edge rules](edge-rules.md).

### Declare access in gregale.yaml

To keep the choice with the code, set `access` at the top of the app's
`gregale.yaml`:

```yaml
access: public   # anyone can open the URL, no Gregale API key
```

`public` does what `--no-require-authn` does; `key` turns the API-key
requirement on (Free has no such requirement, so `key` changes nothing
there). Every production deploy applies the file, including deploys an agent
makes through the Gregale MCP server, so a later `app update` is undone by
the next deploy. Removing the key returns the app to the plan default, which
needs a key again on Hobby and above. An app that never declared `access`
keeps whatever was set by hand. `gregale deploy --plan` shows the declared
access, or how to open the URL when there is none. Deploying to a named
environment leaves access alone, and `basic`, `ip_allowlist` and
`internal_only` are kept. Services of a multi-service project declare it per
service instead ([public or key-protected URLs](multi-service-apps.md#public-or-key-protected-urls)).

## Email verification

A password signup must verify its email address. Until it does, deploying,
billing changes, creating API keys and organizations, and inviting members
answer `email_verification_required`. Open the verification link in the browser
signed in to that Gregale account, click **Verify email**, and wait for
**Email verified**. If the browser is signed in to a different account, choose
**Sign in with the correct account** to return to the confirmation page.
Then run `gregale login` again if you were authorizing the CLI.

If someone else signed up with your address, use **Forgot password** or
request a sign-in link to claim the account. Send a new 24-hour verification
link from the CLI verification prompt, the account page, or with
`gregale account email verify`. After 30
days unverified the account keeps its data, but password sign-in and existing
sessions and keys only reach the account page, logout, deletion, the resend and
the email change (so an address with a typo can be fixed); opening a
verification link, a magic sign-in link, confirming a new address, or signing
in with Google or GitHub at the same address lifts the restriction. Accounts
created before 16 November 2026 get their 30 days from that date
(`api.EmailVerificationEnforcedFrom`). In the last 7 days before the deadline,
an account that is still in use (a signed-in browser or an API key) is mailed
a fresh verification link at most once a day. An account still unverified 60
days after sign-up (counted from 16 November 2026 for older accounts) that
never created an app and has no billing is scheduled for deletion: its
address is mailed and it can be restored for 30 days.

If you never verified and forgot your password, use **Forgot password** anyway:
an unverified account receives a magic sign-in link. Opening it proves the
address, signs out anything the earlier signup created, and lets you set a new
password from the account page.
`gregale org invitation accept TOKEN` opens the invitation in a browser so
acceptance uses your own session and enabled MFA policy, rather than a CLI key.

For shared applications, the current organization role and the credential's
scope both apply. Use an organization-bound key to work on a teammate's app;
see [team application access](organization-app-access.md) for the permission
matrix and the current migration boundary.

## Dashboard OAuth and PKCE

The Google and GitHub dashboard authorization-code redirects use RFC 7636
Proof Key for Code Exchange with the `S256` method. Gregale binds a random
code verifier to the browser callback and sends it to the provider token
endpoint, preventing an intercepted authorization code from being redeemed
without the originating browser. This profile covers dashboard sign-in only;
the separate GitHub App installation callback is not part of the PKCE contract.

## OIDC token exchange

Gregale publishes its supported OAuth token-service capabilities at the RFC
8414 well-known endpoint:

```bash
curl https://api.gregale.dev/.well-known/oauth-authorization-server
```

The metadata advertises the RFC 8693 token-exchange grant, the `deploy:write`
scope, and JWT subject tokens. It intentionally does not claim a general
customer authorization server, authorization-code flow, refresh tokens, or
dynamic client registration.

CI runners can exchange an IdP-issued JWT for a short-lived deploy bearer using
the RFC 8693 form profile. `audience` is the audience your IdP issued the
token for; the first exchange pins it in the account's trust policy:

```bash
curl -X POST https://api.gregale.dev/v1/auth/oidc/exchange \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  --data-urlencode 'grant_type=urn:ietf:params:oauth:grant-type:token-exchange' \
  --data-urlencode 'subject_token_type=urn:ietf:params:oauth:token-type:jwt' \
  --data-urlencode "subject_token=$OIDC_TOKEN" \
  --data-urlencode 'audience=gregale'
```

The response uses the OAuth token shape (`access_token`, `token_type`,
`issued_token_type`, `expires_in`, and `scope`). This RFC 8693 profile remains
deploy-only. The legacy JSON body (`provider`, `token`, and `aud`) also remains
available and defaults to the same deploy-only bearer. Its optional closed
`capability: environment-preflight` profile instead grants only
`project_environments:read` and `project_environments:qualify`. The
environment-preflight GitHub Action uses that profile to qualify a source
release set and check promotion policy without receiving deployment or secret
permissions; see [the Action guide](../.github/actions/environment-preflight/README.md).
`capability: preview-read` grants only `previews:read`: the status of one app's
PR previews (`GET /v1/preview/{slug}` and its environment), and nothing else.
Under a policy Gregale created it is issued to any run of the repository,
pull request runs included, for an app bound to it.

For GitHub Actions, first-use account lookup accepts both the legacy
`repo:OWNER/REPO:...` subject and GitHub's immutable
`repo:OWNER@OWNER_ID/REPO@REPO_ID:...` form. Gregale uses the owner/repository
names only to locate an existing GitHub App binding. It verifies and pins the
complete signed subject, including immutable IDs, in the trust policy.

A trust policy Gregale creates on first use, and every exchange under one,
accepts only the `gregale` audience, so a GitHub token minted for
another relying party cannot deploy. The token's `repository_owner_id` and
`repository_id` claims must equal the IDs the repository binding recorded; a
repository recreated under the same name has to be bound again. Other branches
and workflows of a bound repository are admitted only under a policy Gregale
created: a trust policy the account wrote is authoritative.

Under a policy Gregale created, a deploy bearer follows what the GitHub App
itself deploys:

- The run must be on a branch Gregale deploys from that repository (the
  production branch, or a project deploy branch) or on a `v*` release tag, and
  not a `pull_request` or `pull_request_target` run. A pull request is deployed
  by merging it; its PR preview is built by the GitHub integration.
- The exchange names the app to deploy (`app` in the JSON body, or
  `resource=urn:gregale:app:<app>` in the RFC 8693 form), and the app must be
  bound to the token's repository. The bearer can act only on that app, like a
  per-app deploy token.

A refused exchange answers 403 with the allowed refs. To deploy from another
ref, write the account's trust policy for it. Protect `v*` tags in the
repository if releases depend on review, as you protect the production branch.
## Organization queue permissions

Organization-owned queue operations apply current membership roles alongside
the credential's scopes and MFA checks. Owners, admins and developers can
configure bindings and process messages; viewers can inspect metadata and
counters. Payload inspection requires an operating role. Dead-letter replay
requires an owner or admin. All five roles can read aggregate usage with the
corresponding scope. See [organization queues](organization-queues.md).
