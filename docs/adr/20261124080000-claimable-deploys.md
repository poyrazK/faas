# ADR-20261124080000: Agents deploy without an account, and a person claims the app

- **Status:** accepted (owner, 2026-11-24: "Lets build it that feature end to end in gregale"). Defaults chosen in the owner's absence are marked *assumed*.
- **Date:** 2026-11-24
- **Owner:** apid and CLI maintainers
- **Supersedes:** the "claimable deploys" rejection in [ADR-20261028205920](20261028205920-agent-sign-in.md) (Context). The rest of that ADR stands.
- **Related:** ADR-20261104145353 (sign-up caps), ADR-20261112120603 (CPU abuse), ADR-20261113160000 (sign-up facts), ADR-20261114174600 (held-account links), ADR 20261107103000 (email change), ADR-20261023115741 (capability gate).

## Context

An agent working for someone without a Gregale account cannot deploy
until that person signs up. ADR-20261028205920 considered letting the agent
deploy first and the person claim the app later, and rejected it. The
reasons were a new unauthenticated write endpoint and a pool of unowned
compute (mining, proxies, phishing). It cut sign-in friction instead.

Claimable instances are how other agent-facing platforms grow: an agent
creates the resource, hands its person a link, and the person signs up to
keep it. That moment is the conversion. The owner asked for this flow
before the public launch.

## Decision

### 1. An anonymous deploy creates a real, locked-down account

`POST /v1/anonymous/sessions` (no credential, body `{"accept_terms": true}`)
creates an ordinary account in one transaction:

- Free plan, a personal org and an owner membership, like any sign-up.
- A placeholder address `anon-<20 hex>@unclaimed.invalid`. `.invalid` never
  resolves (RFC 2606), and `pkg/mail` drops any message to it before a
  transport sees it. The account is created verified, because the
  placeholder can never be verified and the account must be able to deploy.
- A row in `unclaimed_accounts`: the SHA-256 of a 32-byte claim token, the
  expiry, the creating address and user agent.
- A deploy key labelled `anonymous-deploy` that expires with the account.
  apid reserves the label: `POST /v1/keys` refuses it, so only this key
  carries it. If the key cannot be minted, the account is deleted, so it
  takes no place under the caps.

The response carries the key, the claim URL and the expiry. Every app,
quota, billing and abuse path already assumes an owning account, so an
unowned-app pool is never created. This answers the first objection: the
only new unauthenticated write is session creation, and it is capped.

Moving an app between accounts was ruled out. 206 tables carry both an
app reference and an account id, so a transfer would touch every one of
them. The claim converts the account in place instead (section 4).

### 2. Until it is claimed, the account may only deploy and read

`unclaimedAllowed` runs in `authLimited` and `authVerifiedLimited`, after
the capability gate. The placeholder address marks the account, so the
check needs no store read. The code lives in `pkg/apid/claim` (ADR-982);
apid keeps the route table, the middleware and a `claim.Host` adapter.

- Allowed: reads under `/v1/apps`, `/v1/deployments`, `/v1/uploads`,
  `/v1/projects`, `/v1/builds`, `/v1/account`, `/v1/me`, `/v1/whoami`,
  `/v1/usage` and `/v1/runtimes`.
- Allowed writes are listed route by route (`claim.RouteAllowed`): create,
  deploy (including source tarballs and staging), cancel, roll back,
  restart and delete an app; source uploads; the app's environment
  variables and secrets; and `POST /v1/account/claim-link`. A test keeps
  the list in step with the route table.
- Reads under those roots exclude routes that act: a WebSocket upgrade
  is refused as a class, durable-entity connections and fork execs are
  named, and reads of keys, tokens and credentials (any path with a
  credential segment such as `keys`, `deploy-tokens` or `credentials`)
  are refused like `GET /v1/keys`.
- `POST /v1/apps` and `PATCH /v1/apps/{slug}` are allowed with only the
  settings a deploy applies (`claim.RefusedField`; creating also takes
  `slug`, `type`, `runtime` and `vcpu`): public access (`require_authn`,
  `public_auth`, `platform_tenant_required`), protocol, size, timeouts,
  replicas and restart behaviour. Any other field, such as an egress allow
  list, service bindings or minimum instances, answers `403
  claim_required` naming the field.
- Project deploys (`/v1/projects` scan and apply) are refused: they create
  databases and several apps. The CLI refuses `gregale deploy --project`
  (and the other project flags) without a login before it starts a
  session.
- Every other request answers `403 claim_required`: deny by default, so a
  new route is closed to unclaimed accounts until it is added on purpose. A
  block list by path segment was rejected: it let app-scoped credentials
  (AI-gateway tokens, realtime publish keys), log drains and environment
  clones through, and those outlive the claim. The gate sits in every
  authenticated middleware chain, the plain one included, so account
  routes (delete, restore, email change, sessions) are closed too.
- The Free plan's own limits apply unchanged: one app, the Free RAM cap and
  no cron. The ADR-20261112120603 CPU rule already enforces on Free.

### 3. Caps, expiry and abuse

| Setting (runtime config, hot) | Default | Meaning |
|---|---:|---|
| `unclaimed_deploy_ttl_hours` | 72 | claim window; 1–168 |
| `unclaimed_accounts_live_max` | 500 *assumed* | unclaimed accounts alive at once; 0 closes |
| `unclaimed_accounts_per_address_per_day` | 3 *assumed* | per IP, last 24 h; 0 closes, even for a request without a usable address |
| `unclaimed_accounts_per_network_per_day` | 10 *assumed* | per /24 or /48, last 24 h; 0 closes, even for a request without a usable address |

- The caps are checked in the transaction that creates the account,
  under a transaction-scoped advisory lock, from `unclaimed_accounts`
  itself (`created_ip`, `created_at`). Concurrent requests and several apids
  therefore cannot exceed them, and a refused request creates nothing. They
  are separate from human sign-ups, so anonymous deploys never use up a
  network's sign-ups. Unclaimed accounts are left out of the platform-wide
  daily sign-up count (`CountVerifiedAccountsCreatedSince`) until claimed.
- A refusal is `429 anonymous_deploys_unavailable` with `Retry-After` and
  the `limit` and `observed` values of the cap that refused it.
- A failed count fails the creation. Anonymous deploys are optional;
  sign-up admits on the same failure.
- Every session records `account_signup_facts` (method `anonymous`). If the
  ADR-20261114174600 check links it to an abuse-held account, the new
  account is ended at once and the caller gets `429
  anonymous_deploys_unavailable`. Its claim window is ended first
  (`EndUnclaimedWindow`), so no claim lands on it and the next sweep
  retries a deletion that fails. The sign-up itself is counted only once
  the session is granted. Sign-up only flags such links; an account nobody
  has identified gets the stricter rule.
- apid sweeps every 5 minutes, paging through due rows by
  `(expires_at, account_id)`, so rows it cannot finish never hold back the
  ones after them. A row is due only once its expiry has passed by both
  apid's clock and the database's. Claim confirmations expire by the
  database clock no later than the claim window, so once the sweep lists a
  row no claim can land on it. A claim that landed earlier is seen when the
  sweep reads the account, and the sweep finishes it instead of deleting. An open account past its expiry goes
  through the usual `scheduleDeletion` (via `unclaimed_expiry`) first, and
  only then is the row marked `expired_at`: a transient failure leaves the
  row open for the next sweep. A refusal that will not change (a suspended
  or abuse-held account, whose hold owns it) still ends the claim window.
  Deletion stops its apps and keys and starts the normal erasure pipeline;
  the placeholder receives no notice. Marking the row also drops pending
  claim confirmations.
- Metric `apid_unclaimed_accounts_total{event}`: created, claimed, expired,
  refused_network, refused_live_cap, refused_held_link. Audit events:
  `account.unclaimed_created`, `account.unclaimed_refused`,
  `account.claim_requested`, `account.claimed`, `account.unclaimed_expired`.

### 4. The claim converts the account in place

The claim pages are served by apid on the API host, which routes `/claim`
to apid (`apid.ApidRootClaim`; it is reserved on platform hosts only):

1. `GET /claim/{token}` lists the apps, the deletion time and an email form.
2. `POST /claim/{token}` issues an email change from the placeholder to the
   given address (the ADR 20261107103000 flow) and mails a confirmation
   link. The change is issued in the request, in one transaction that locks
   the live claim row (`IssueClaimEmailChange`), so a link rotation or
   expiry runs wholly before it (and the page answers 410) or after it (and
   drops it). Only the mail is sent in the background; if apid is too busy
   to queue it, the page answers 503 rather than claim a mail was sent. The answer is the same whether or not the address has an account.
   An address that already has an account gets an explanation instead of a
   link.
   The pages send `Referrer-Policy: same-origin`, so the browser's form
   posts carry this host's Origin for the trusted-origin check. Errors on
   these pages (a malformed address, a used or expired confirmation, an
   address another account took) are pages too, not JSON.
3. `GET /claim/confirm` renders a button. Mail scanners that prefetch links
   consume nothing.
4. `POST /claim/confirm` consumes the change. The store refuses an address
   another account took meanwhile, and an account that is no longer active.
   `finishEmailChange` sees the placeholder as the old address and calls
   `claim.Service.FinishClaim`: the `anonymous-deploy` keys are revoked
   first (keys the owner made since are left alone, even when the sweep
   finishes a claim late), and only then is the row marked `claimed_at`. The browser then
   gets a one-time magic-link sign-in and lands on the dashboard as the
   owner.

The confirmation is bounded three ways. It expires after an hour or at the
end of the claim window, whichever is first. `POST /v1/account/claim-link`
drops pending confirmations together with the old link, so a confirmation
mailed through a leaked link stops working once the agent replaces it.
Expiry drops them too.

If the address moved but recording the claim failed, the row stays open.
Two things cover that gap. The middleware refuses an `anonymous-deploy` key
on an account that has an `unclaimed_accounts` row but no placeholder
address. And every sweep (every 5 minutes) finishes open rows whose
account no longer has the placeholder address, so the claim is recorded
within minutes, not at the end of the window. The anonymous key also expires with
the claim window.

The account, its apps, deployments and data stay where they are. The agent's
key stops working; the agent signs in as the person (`gregale login`) to
keep deploying.

*Not in this ADR:* claiming into an account that already exists. It needs
app transfer between accounts (see section 1). Such a person can claim with
another address, or sign in and redeploy.

### 5. The CLI deploys anonymously for agents

`gregale deploy` with no saved login and no `GREGALE_TOKEN`:

- deploys anonymously when a known coding agent runs it (its marker from
  `api.CodingAgents` is set: Claude Code, Codex, Gemini CLI, Cursor) or
  when `--anonymous` is given;
- does not when `CI` is set or `GREGALE_NO_ANONYMOUS_DEPLOY` is set. A job
  without a terminal is not enough: cron, Jenkins and build servers often
  set no `CI`, and one that lost its token must fail, not deploy somewhere
  nobody owns.

It keeps the session's key in `anonymous-deploy.json` next to the profile's
token file, never in the login store. Only `gregale deploy` and `gregale
claim` use it, only while no login exists, only for the same API, and only
until the claim window ends; other commands see no login. A redeploy checks
the key first; a refused key (the app was claimed) is deleted and the CLI
says to run `gregale login`. `gregale logout` removes it too. A redeploy with the saved key repeats
its claim link after a successful deploy (a failed deploy prints none),
and `gregale claim` saves the link it issues. The CLI prints the claim link before the upload
and again after the deploy. `gregale claim` prints a fresh
link; the previous link stops working. After the window ends it answers
`410 claim_expired`. A server without the capability
answers 404, and the CLI shows the usual "not logged in" error.

### 6. Rollout

The routes belong to the `claimable-deploys` capability (preview). Anonymous
sessions are offered only when its flag is on for callers no rule names
(`ReleasedByDefault`). Turning the flag off stops new sessions at once;
existing unclaimed accounts keep their expiry. `unclaimed_accounts_live_max
= 0` closes new sessions without touching the flag.

## Consequences

- Unclaimed apps are served from `<slug>.<apps domain>` like any Free app.
  An agent-created app could still host phishing for up to the TTL. The
  bounds are one app, Free RAM, no custom domain, the caps and the held-link
  refusal. A noindex header and an "unclaimed" banner on app responses are
  follow-ups that need gateway work.
- Placeholder accounts appear in operator views (`gregalectl accounts risk`)
  with their sign-up facts.
- All caps are global, unlike the sign-up caps, which are per apid
  process.
- Claiming into an existing account and app transfer remain open.
