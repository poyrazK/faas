# ADR-20261019102657: Per-account realtime preview rollout and realtime apps declared in gregale.yaml

- **Status:** accepted (owner direction, 2026-10-19: "Lets build those then" after the realtime next-steps review)
- **Date:** 2026-10-19
- **Decision:** Realtime preview features become visible per account, through one allowlist, so the owner can test them on production before customers see them. A whole realtime app can then be declared in `gregale.yaml`: endpoints by slug, their Gregale Auth sign-in, consumer groups, channel rules and the triggers that consume them. Channel rules evaluated by realtimed let common channels skip the authorize callback.
- **Why:** Everything built for realtime since ADR-369 is off for every account (operator flags, default `0`, set nowhere in `deploy/`), and turning a flag on shows it to every customer at once. Building a realtime app still means creating endpoints and consumer groups by hand and pasting endpoint IDs into trigger configs, and every retained subscription asks a callback every 30 seconds, which keeps the app (or its Auth app) awake.

## 1. Per-account preview rollout

`FAAS_REALTIME_PREVIEW_ACCOUNTS` on apid: comma-separated account IDs, `*` for every account, empty (the default) for none, parsed like `FAAS_LIVE_QUERIES_ACCOUNTS` (ADR-20261016151947). apid consumes it through an account-decided gate interface, so when platform flags (ADR-1700, PR #4893) land, a `realtime_preview` flag replaces it at runtime with this value as its version-0 fallback, as they do for live queries. realtimed never reads the list: apid marks each endpoint, so realtimed needs no flag watcher.

- **What it covers.** The customer-visible preview surfaces: retained history and everything behind `FAAS_REALTIME_RETAINED_PREVIEW_ENABLED` (consumer groups, inbox, push, schedules, snapshots, reducers, read progress, realtime triggers), subscription tickets, principal access controls, channel occupancy, the usage API, and on realtimed the v2 protocol (resume, presence, signals), SSE, tickets and live occupancy.
- **Flags stay.** Each existing `FAAS_REALTIME_*` flag remains the operator's switch for "this daemon is ready" (readers configured, migrations applied). A preview feature is visible to an account only when its flag is on **and** the account is in the allowlist.
- **apid.** Outside the allowlist, preview routes answer exactly what an unregistered route answers (`httpkit.NoSuchRoute`): before authentication while nobody is listed, right after authentication for an unlisted account. Publisher and worker credentials stop authenticating. Trigger creation treats `kind: realtime` as an unknown kind, and `delivery=retained` on the generally available publish and principal-send routes is answered like any invalid delivery value.
- **realtimed.** apid registers each endpoint with `preview: true` only for allowlisted accounts. realtimed applies a preview feature to an endpoint only when its own flag is on and the endpoint is marked. An older apid sends no mark, so preview stays off; an older realtimed ignores the mark and keeps its global flags, which are off.
- **Not covered.** Platform protections stay global operator flags: fair use, the outbound budget, usage recording and trigger-ledger retention (ADR-1323). They hold no customer surface of their own. `delivery: batch` (ADR-1321) is released per account by the `trigger_batch_delivery` platform flag in ADR-1700. Background workers (schedules, push, replay jobs, retention) keep following the daemon flags; an account outside the rollout has no rows for them.
- **Discovery** (served OpenAPI, CLI help) follows ADR-20261018090210 in a later slice; until then the paths stay in the spec and SDKs but answer as absent.

## 2. Realtime apps in gregale.yaml

```yaml
realtime:
  endpoints:
    - slug: chat
      auth: shop-auth                 # a Gregale Auth app in the same account
      allowed_origins: [https://shop.example.com]
      max_connections: 1000
      channels:                       # §3, optional
        - pattern: "org:{org_id}:room:*"
          read: true
      consumer_groups:
        - channel: orders
          name: billing
          lease_seconds: 120
triggers:
  - kind: realtime
    slug: orders
    config: {endpoint: chat, channel: orders, group: billing}
```

- **Endpoint slugs.** Endpoints get an optional `slug`, unique per app (`[a-z0-9-]{1,48}`). API-created endpoints may stay without one, and `PATCH` can add, rename or clear a slug. A manifest owns the app's slugged endpoints; endpoints without a slug are never touched by a deploy.
- **Reconcile.** The CLI deploy reconciles the app's slugged endpoints before its realtime triggers, inside the same rollback transaction (as ADR-1322 does for triggers): it creates and updates declared endpoints and **disables** slugged endpoints the file no longer declares. It never deletes an endpoint or a consumer group, because both hold messages a rollback could not restore; consumer groups are created when missing and otherwise managed through their own API. Declaring `realtime:` opts the app in; omitting it leaves realtime alone. GitOps (source-ref) deploys apply neither realtime triggers nor endpoints yet.
- **Callback token.** A new `auth: none` or `oidc:` endpoint's callbacks go to the app's own HTTPS origin; the deploy generates the callback token and stores it as the app secret `REALTIME_<SLUG>_CALLBACK_TOKEN`, then restarts the app fresh to load it. A rollback that undoes a changed auth app cannot restore the earlier callback credential.
- **Auth.** `auth: <auth app>` does what `gregale realtime create --auth` does: resolve the Auth app's issuer, set `oidc_jwt` with ES256 and audience `authenticated`, seal a callback token into the Auth app and restart it fresh. It is refused while Application Auth is dark-launched (ADR-1190). `auth: none` and an explicit `oidc:` block (issuer, JWKS URL, audience, algorithms) cover the other modes. Static bearer tokens are not declarable, because they are secrets.
- **Triggers by slug.** A realtime trigger's `config.endpoint` may name an endpoint slug of the same app instead of `endpoint_id`.
- **Preview deployments** keep using the production endpoints in this version.

## 3. Channel rules

An endpoint may list rules that realtimed evaluates from the verified JWT, with no callback:

```yaml
channels:
  - pattern: "org:{org_id}:room:*"   # {claim} must equal the token's claim
    read: true
    presence: true
  - pattern: "user:{sub}:inbox"
    read: true
```

- **Matching.** A pattern is literal text, `{claim}` segments filled from the token (`sub`, `org_id`, `org_role`, `session_id`, or any string claim), and a trailing `*`. The first matching rule decides. A rule grants only what it lists (`read`, `presence`).
- **Fallback.** A channel no rule matches goes to the authorize callback as today, if the endpoint has one, and is otherwise denied.
- **Trade-off, chosen per rule.** A rule trusts the token until it expires (at most 600 seconds for Gregale Auth), so a sign-out or membership change is enforced within the token's lifetime instead of within the 30-second callback refresh. Channels that need faster revocation keep the callback, and principal access controls still revoke immediately.
- **Limits.** At most 32 rules per endpoint, patterns up to 256 bytes (`pkg/api/limits.go`).

## Rejected alternatives

- **One global flag per feature, turned on at launch.** That is today's state: the owner cannot test on production without exposing the feature to everyone.
- **A per-account allowlist on realtimed.** realtimed would need account metadata it does not have; apid already knows the account when it registers an endpoint.
- **Endpoints identified by ID in the manifest.** IDs differ per environment and are unknown until creation, so a manifest could not be applied twice or to a fresh account.
- **Channel rules in the Auth app's policy language.** That still needs a callback per check, which is what keeps the Auth app awake.

## Consequences

- Turning a preview feature on for production testing means listing an account, not exposing everyone; launch is `*`.
- A realtime app (endpoint, sign-in, consumer groups, triggers) deploys from one file and can be redeployed or recreated in another account.
- Rule-authorized channels no longer wake the app or Auth app for authorization; their revocation latency is the token lifetime.
- Migrations: an expand migration adds `managed_realtime_endpoints.slug` and `channel_rules` (jsonb).
- Work is sliced: (1) rollout gate, (2) slugs and the `realtime:` manifest section, (3) server-side Gregale Auth wiring, (4) channel rules in realtimed, (5) a native end-to-end scenario, (6) hiding preview paths from the served OpenAPI and CLI help.
