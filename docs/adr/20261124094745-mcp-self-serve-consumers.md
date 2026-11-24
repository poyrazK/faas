# ADR-20261124094745: Self-serve consumers for MCP servers

- Status: Proposed (dark behind the `mcp_gateway_auth` platform flag and an
  app opt-in; hidden preview CLI)
- Date: 2026-11-24
- Related: ADR-120 (consumer keys), ADR-953 (consumer plans), consumer
  sign-up (ADR-20261021183000), ADR-20261122194324 (MCP gateway auth modes),
  ADR-20261123192254 (MCP per-tool billing and plan route limits)

## Context

An MCP server owner can now price tools, cap them per plan and let people
sign in through `oauth-proxy` (Gregale Auth or any OpenID Connect issuer).
Three things still stop a stranger from finding the server, using it free and
paying for more without the owner doing anything:

1. **OAuth callers are nobody.** Plans, caps, per-tool billing and statements
   apply to consumers (`api_consumers`). A caller signed in through
   `oauth-proxy` carries a JWT, not a consumer key, so the gateway admits it
   unmetered. MCP clients (Claude, Cursor, ChatGPT) connect with OAuth; most
   users never paste a key.
2. **Free still needs a card.** Consumer sign-up always runs a Stripe
   Checkout that saves a card, even for a free plan.
3. **A cap is a dead end.** A `429 consumer_plan_limit_exceeded` names the
   limit but not how to raise it.

## Decision

Owner choices (2026-11-24): OAuth sign-ins become consumers, the api-key
sign-up page stays, and an OAuth user links to an existing key consumer by
proving the key; a free plan needs no card; cap errors carry an upgrade
link; an OAuth consumer is identified by issuer + subject.

### 1. OAuth consumers

- **Opt-in.** `gregale consumers signup-set APP --oauth-consumers` turns it
  on for an app whose MCP policy is a bearer-token `oauth-proxy` policy. It
  needs a free plan (below), which new OAuth consumers join.
- **Identity.** New table `api_consumer_identities` (account, app, consumer,
  issuer, subject, email, created_at), unique on (app, issuer, subject). The
  issuer is the rule's pinned issuer; the subject is the verified token's
  `sub`. Email is display only and never used to match.
- **Enrollment.** apid owns consumers, so the gateway never writes them.
  After the MCP JWT rule verifies a token, the gateway looks the identity up
  (cached 5 minutes, negative 30 s). On a miss it calls a new apid
  `Auth.EnrollOAuthConsumer` RPC with app, issuer, subject and email. apid
  creates the consumer (`external_ref` = `oauth:<issuer>#<sub>`, bounded to
  256 bytes by hashing a long subject), the identity, and a plan assignment
  to the free plan, in one transaction, idempotently on (app, issuer,
  subject). It returns the consumer ID.
- **Bounds.** Enrollment counts against the sign-up daily limit
  (`--daily-limit`, default 100, at most 1,000). Over the limit, or with
  apid unreachable or older than this RPC, the gateway refuses with `503
  consumer_enrollment_unavailable` and `Retry-After`: an opted-in app never
  serves unmetered OAuth traffic.
- **Attribution.** The enrolled consumer becomes the request's
  `Authenticated.ConsumerID`. The request then gets a billing route like any
  consumer request, so tool labels, plan caps, statements, spend caps and
  overdue holds apply. A revoked consumer is refused with `403`, the same as
  a revoked key.

### 2. Card-less free plan

- `signup-set --free-plan PLAN` names one offered plan as free. apid accepts
  it only while the plan's rate card in force charges nothing: no unit price
  outside the included units, no priced tier, no base fee and no minimum.
  A later rate card that charges on that plan is refused while it is the free
  plan.
- On the sign-up page and `POST /v1/consumer-signup/APP`, choosing the free
  plan skips Stripe Checkout and returns the key at once. The usual limits
  still apply (10 sign-ups per address an hour, the app's daily limit).
  The `consumer.signed_up` webhook reports `provider_customer_id` empty.
- Upgrading from the free plan to a paid plan runs Checkout then, which
  creates the Stripe customer and saves the card (below).

### 3. Upgrade link

- A plan-limit `429` (`consumer_plan_limit_exceeded`, including route and
  tool caps) gains `upgrade_url` when the app has sign-up on with more than
  one offered plan. On an MCP JSON-RPC request the same URL is in the error's
  `data.upgrade_url`, which MCP clients show to the user. (Implementation
  note: the first slice puts it in the problem body only; the JSON-RPC
  `data` copy is a follow-up.)
- A customer without a card (a `signup:` or `oauth:` external_ref) cannot
  switch itself to a paid plan with its key; the upgrade page is the way.
- The URL is `/v1/consumer-signup/APP/upgrade?token=…`. apid mints the token
  (`Auth.ConsumerUpgradeLink`, cached by the gateway per consumer for 10
  minutes): an HMAC over (consumer, app, expiry) with apid's sign-up secret,
  valid 24 hours. It only opens the hosted upgrade page for that consumer; it
  grants no API access.
- The page lists the offered plans. A paid plan runs Stripe Checkout (setup
  mode) for that consumer, creating its Stripe customer if it has none, then
  assigns the plan from the next minute through the existing self-service
  plan change. It needs `--customer-plan-changes`; without it the URL is
  not sent.

### 4. Linking by key

- A signed-in OAuth user who already has a key consumer for the same app
  sends `POST /.well-known/gregale-consumer-link<endpoint path>` on the
  app's host, with the OAuth access token as bearer and `{"key": "ck_…"}`.
  (Implementation note: the draft put this on apid at
  `/v1/consumer-signup/APP/link`, but apid cannot verify tokens of an
  arbitrary external issuer; the gateway already verifies them for the MCP
  rule.) The gateway verifies the token against the app's MCP rule (issuer,
  audience, signature, endpoint scopes) and calls a new apid
  `Auth.LinkOAuthConsumer` RPC; apid verifies the key against the app's
  consumers (live, unexpired, hash match) and, in one transaction, points
  the identity at the key's consumer and revokes the consumer enrollment
  created; the closed consumer's external ref becomes `linked:<id>`, so an
  unlinked identity enrolls afresh, while a consumer the owner revoked
  keeps its ref and stays refused. Its billed usage stays on that closed
  consumer's statements.
  The answering gateway bills the next call to the key's consumer at once;
  other gateways do when their 5-minute cache entry expires.
- One identity links to at most one key consumer; relinking needs the
  owner: `gregale api consumer identity list APP` lists identities and
  `gregale api consumer identity delete APP ID` deletes one
  (`GET`/`DELETE /v1/apps/APP/consumer-identities[/ID]`). An unlinked
  identity enrolls a new free consumer on its next call.
- No email matching: sign-up emails are typed in and never verified, so a
  stranger could claim a victim's address.

## Compatibility

- Expand-only migration: the new identity table, and sign-up settings
  columns `oauth_consumers boolean NOT NULL DEFAULT false` and
  `free_plan_id uuid` (CHECKs `NOT VALID`).
- New RPCs on `Auth`; an old apid answers `Unimplemented`, which the gateway
  reads as "this app enrolls nobody". The gateway cannot tell an opted-in app
  from others before apid answers, so it asks for every MCP OAuth caller of a
  monetized account and caches "off" per app for 5 minutes. When apid cannot
  answer, an app that enrolled a caller in the last 24 hours fails closed
  (`503`); any other app keeps serving its callers unattributed, as before
  this ADR. Deploy order: data → apid → gateway.
- `upgrade_url` and `data.upgrade_url` are new optional fields.

## Consequences

- An MCP server can be listed publicly: anyone signs in, gets the free plan,
  hits a cap and upgrades, with no owner action.
- The first request of each new OAuth user waits for one apid round trip.
- Owners pay nothing for free-plan users beyond their own app's compute;
  the daily enrollment limit bounds sign-up abuse.
- Identities survive token rotation and email changes; a user who signs in
  through a different issuer is a different consumer unless linked by key.
