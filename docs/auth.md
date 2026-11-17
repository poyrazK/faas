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

For CI, create a scoped project or organization token and expose it as `FAAS_TOKEN`, or pass it once with `gregale login --token "$FAAS_TOKEN"`. Prefer the narrowest scope and a short expiration. Do not commit tokens or pass them as command-line arguments where process listings can expose them.
Tokens supplied through `FAAS_TOKEN` or `gregale login --token` remain
non-owning: logout clears local state but does not revoke a shared CI key.
`FAAS_TOKEN` outranks a saved login, so `gregale login` and `gregale start`
refuse to run while it is set (unless `--token` passes the same value), and
`gregale logout` says when `FAAS_TOKEN` is still active.

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

## Email verification

A password signup must verify its email address. Until it does, deploying,
billing changes, creating API keys and organizations, and inviting members
answer `email_verification_required`. The verification link works only in a
browser signed in to that account: opened anywhere else, it asks you to sign
in first and then verifies. (Someone who signed up with your address cannot
verify their account by getting you to click the link; if you did not create
the account, use **Forgot password** or a sign-in link instead, which claims
it.) Send a new 24-hour link
with `gregale account email verify` or the account page's button. After 30
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
