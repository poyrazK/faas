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
check needs no store read.

- Allowed: reads under `/v1/apps`, `/v1/deployments`, `/v1/uploads`,
  `/v1/projects`, `/v1/builds`, `/v1/account`, `/v1/me`, `/v1/whoami`,
  `/v1/usage` and `/v1/runtimes`, and writes under them.
- Refused with `403 claim_required`: writes whose path names domains,
  certificates, databases, caches, buckets, keys, tokens, billing, plans,
  orgs, members, crons, webhooks, email, mirrors, egress, private networks,
  TCP/UDP, forks, sandboxes, consumers, MCP or agents. Every other root is
  refused too: deny by default. Under `/v1/account` the only allowed write
  is `POST /v1/account/claim-link`.
- The Free plan's own limits apply unchanged: one app, the Free RAM cap and
  no cron. The ADR-20261112120603 CPU rule already enforces on Free.

### 3. Caps, expiry and abuse

| Setting (runtime config, hot) | Default | Meaning |
|---|---:|---|
| `unclaimed_deploy_ttl_hours` | 72 | claim window; 1–168 |
| `unclaimed_accounts_live_max` | 500 *assumed* | unclaimed accounts alive at once; 0 closes |
| `unclaimed_accounts_per_address_per_day` | 3 *assumed* | per IP, per apid |
| `unclaimed_accounts_per_network_per_day` | 10 *assumed* | per /24 or /48, per apid |

- The per-network limiter is separate from human sign-ups, so anonymous
  deploys never use up a network's sign-ups. Unclaimed accounts are left
  out of the platform-wide daily sign-up count
  (`CountVerifiedAccountsCreatedSince`) until they are claimed.
- A failed live count refuses. Anonymous deploys are optional; sign-up
  admits on the same failure.
- Every session records `account_signup_facts` (method `anonymous`). If the
  ADR-20261114174600 check links it to an abuse-held account, the new
  account is ended at once and the caller gets `429
  anonymous_deploys_unavailable`. Sign-up only flags such links; an account
  nobody has identified gets the stricter rule.
- apid sweeps every 5 minutes. An open account past its expiry is marked
  `expired_at` and goes through the usual `scheduleDeletion` (via
  `unclaimed_expiry`). That stops its apps and keys and starts the normal
  erasure pipeline; the placeholder receives no notice.
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
   link. The answer is the same whether or not the address has an account.
   An address that already has an account gets an explanation instead of a
   link.
3. `GET /claim/confirm` renders a button. Mail scanners that prefetch links
   consume nothing.
4. `POST /claim/confirm` consumes the change. The store refuses an address
   another account took meanwhile. `finishEmailChange` sees the
   placeholder as the old address and calls `completeClaim`: the row is
   marked `claimed_at` and every API key on the account is revoked. The
   browser then gets a one-time magic-link sign-in and lands on the
   dashboard as the owner.

The account, its apps, deployments and data stay where they are. The agent's
key stops working; the agent signs in as the person (`gregale login`) to
keep deploying.

*Not in this ADR:* claiming into an account that already exists. It needs
app transfer between accounts (see section 1). Such a person can claim with
another address, or sign in and redeploy.

### 5. The CLI deploys anonymously for agents

`gregale deploy` with no saved login and no `GREGALE_TOKEN`:

- deploys anonymously when it has no terminal (an agent's shell) or when
  `--anonymous` is given;
- does not when `CI` is set or `GREGALE_NO_ANONYMOUS_DEPLOY` is set. A CI job
  that lost its token must fail, not deploy somewhere nobody owns.

It saves the session's key like a login, prints the claim link before the
upload, and prints it again after the deploy. `gregale claim` prints a fresh
link; the previous link stops working. A server without the capability
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
- The per-address limits are per apid process, like the sign-up caps; the
  live cap is global.
- Claiming into an existing account and app transfer remain open.
