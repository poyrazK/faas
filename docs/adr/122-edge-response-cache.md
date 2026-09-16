# ADR-122 · Edge response cache (`kind=cache`)

- **Status:** **Proposed** (PR pending; branch
  `worktree-adr-122-edge-response-cache`)
- **Date:** 2026-08-20
- **Decision:** A new edge-rule kind, `kind=cache`, gives customers
  an opt-in per-route HTTP response cache at the gateway. Rules
  declare a path glob, a method set, a `max_age`, an optional
  `stale_if_error` window, and a `vary_on` header list. The cache
  lives **in-process per `gatewayd-internal`** (bounded LRU, no
  persistence, no shared store) and sits in the middleware chain
  **after the auth gates and before the wake gate** — so a hit can
  never bypass authentication, and a hit never wakes a microVM.
  **Requests carrying credentials are never cached** (hard bypass);
  `vary_on` is restricted to non-credential headers. Cache hits that
  displace a cold boot are counted as avoided wakes, which makes
  "saved compute cost" a derived figure rather than an estimate.

## Context

Customers running read-heavy public endpoints (catalog listings,
docs pages, public JSON APIs) currently pay a full microVM wake for
every request that arrives while the app is parked. The platform
has no HTTP response cache anywhere in the request path: no
`ETag`, no `If-None-Match`, no `Cache-Control` handling on proxied
responses, no `Vary`, no stale-serving. The 13 existing edge-rule
kinds (`route`, `rewrite`, `redirect`, `headers`, `cors`, `jwt`,
`ip`, `validate`, `limit`, `maintenance`, `geo`, `throttle`,
`budget` — `pkg/state/types.go`) cover routing, security and
admission, but nothing on the response side.

The caches that do exist are all control-plane metadata, not
customer response bodies: `RouteCache` (hostname → app),
`EdgeRuleCache` (compiled per-host rule subsets), `PublicAuthCache`
(unsealed Basic-auth credentials), `WarmHintCache`. The build
cache in `builderd` is a build-time layer cache. None of them
stores a customer response.

### Why this belongs on a scale-to-zero platform specifically

On a conventional platform a response cache saves latency and
origin CPU. On Gregale it additionally **avoids the wake itself**.
A request that lands on a parked app normally goes through
`WakeGate.Wait` → schedd admission → snapshot restore, and the
restored instance then accrues billing at plan RAM + 8 MB per
running second (§4.7). A cache hit served before the wake gate
means **no VM runs at all**, so no `gb_ram_hour` accrues.

That makes the saved-cost figure *derivable* rather than modelled:

```
saved_cost = wakes_avoided × (plan_ram_mb + 8) × billed_seconds
```

This is a stronger claim than a conventional CDN can make, and it
is the primary justification for accepting the architectural
deviation below.

### The constraint this deviates from

`docs/faas_implementation_spec.md` §17 states the platform is
**stateless by contract**: "Customers bring their own datastore /
cache / object store / APIs." `docs/storage.md` points customers at
Upstash Redis / KV for caching. A platform-owned response cache is
the **first customer-visible exception** to that posture, which is
why this needs an ADR rather than a quiet feature addition.

The deviation is deliberately bounded:

- **Opt-in and default-off.** No app gets a cache without an
  explicit `kind=cache` rule. Zero behaviour change for every
  existing app.
- **In-memory only.** No customer response body is ever written to
  disk or to Postgres. The cache dies with the process. This keeps
  the GDPR/retention surface at zero and avoids a new durable
  failure domain.
- **Never authoritative.** A miss always falls through to the
  existing wake path. This mirrors ADR-005's "snapshots are cache,
  not truth" posture: the cache is an optimisation, never a
  correctness dependency. If the cache is empty, cold, or disabled,
  the platform behaves exactly as it does today.
- **Not a KV replacement.** The bring-your-own-cache guidance in
  `docs/storage.md` stands. This caches *responses the platform is
  already proxying*; it is not a general-purpose store customers
  can write to.

## Decision

### D1 — A new edge-rule kind, not a new subsystem

Every knob the feature needs already exists and is exercised by 13
sibling kinds: the path-glob + method matcher (`pathGlobMatch`),
the per-host compiled rule cache with `pg_notify` invalidation, the
per-plan quota gate inside `CreateEdgeRuleIfUnderQuota`'s
`FOR UPDATE` lock, the closed-vocabulary CLI, and the
`gateway_edge_rule_match_total{kind,outcome}` metric family.
Modelling the cache as `kind=cache` inherits all of it and keeps
the customer's mental model uniform ("rules apply to path globs").

Rule action shape:

```json
{
  "max_age_seconds":        60,
  "stale_if_error_seconds": 300,
  "vary_on":                ["Accept-Language"],
  "methods":                ["GET", "HEAD"]
}
```

### D2 — Placement: after the auth gates, before the wake gate

`ServeHTTP` order is load-bearing in both directions:

```
… → applyEdgeRuleCORS  (returns early on preflight)
  → applyEdgeRuleJWT → applyEdgeRuleIP → applyEdgeRuleGeo
  → applyEdgeRuleLimit → applyEdgeRuleThrottle
  → applyEdgeRuleValidate → applyEdgeRuleBudget
  → enforceRequireAuthn
  → enforcePublicAuth
  → applyEdgeRuleCache      ← SERVE (new)
  → … → gate.Wait           ← wake
```

- **After `enforceRequireAuthn` / `enforcePublicAuth`.** A cache hit
  must never bypass authentication. If the applier ran earlier, an
  app protected by `public_auth` would serve cached bodies to
  unauthenticated callers — a security regression. Running after
  means both gates have already accepted or rejected the request.
- **Before `gate.Wait`.** This is the entire economic point. A hit
  must return without entering the wake gate so no VM is admitted.
- **After `applyEdgeRuleCORS`.** CORS returns early on preflight, so
  OPTIONS responses never reach the cache. A preflight cached
  against the wrong `Origin` would be a real CORS bypass.

### D3 — Credentialed requests are never cached

The obvious reading of "vary by `Authorization`" is to include the
credential in the cache key. **We reject that for v1.** A shared
cache keyed on credentials is the classic cross-tenant leak: a
single keying bug serves principal A's response body to principal
B. It also destroys the hit rate it purports to enable — one entry
per token per path means near-zero reuse and unbounded cardinality
under token rotation.

Instead: the presence of an `Authorization` header or a session
cookie is a **hard bypass**. The response is neither stored nor
served from cache; the request proceeds to the origin exactly as it
does today, counted as `outcome="bypass_authed"`.

This is correct by construction — an authed response that is never
stored cannot leak — rather than correct by careful keying. It is
the difference between a safety property and a safety argument.

`vary_on` therefore accepts only non-credential dimensions
(`Accept-Language`, `Accept-Encoding`, and query parameters).
Per-principal caching is deferred to a follow-on ADR; it needs a
principal-resolution hook (ADR-104's authenticated-context carrier),
private-entry semantics, and revocation-driven eviction, none of
which should ride along with the base mechanism.

### D4 — Cacheability is deny-by-default

Store only when **all** hold: method ∈ {GET, HEAD}; no
`Authorization` and no session cookie on the request; status ∈
{200, 203, 300, 301, 308, 404, 410}; no `Set-Cookie` on the
response; no `Cache-Control: no-store|private` on the response;
body within the per-entry cap (1 MiB).

An origin `Cache-Control: no-store` / `private` is an **absolute
veto** — the app can always opt a route out even when a rule
matches it. The rule expresses the operator's intent; the origin
header expresses the application's, and the application wins.

Key shape:

```
appID | ruleID | deploymentID | method | normalizedPath
      | h(vary_on values) | sortedQuery
```

`appID` prevents cross-app serving. `deploymentID` means a new
deploy cannot serve the previous release's bodies — a correctness
property that TTL alone would not give.

### D5 — Stale-on-failure is failure-only

Entries are retained past `max_age` for up to `stale_if_error`
(capped at 5 minutes). Stale content is served **only** on a
genuine origin failure — wake failure from `gate.Wait`, upstream
5xx, or upstream timeout — and **never** on an ordinary cache miss.
Stale responses carry `Warning: 110 - "Response is Stale"` and are
counted as `outcome="stale_if_error_served"`, distinct from fresh
hits, so a degraded origin never inflates the advertised hit rate.

### D6 — Saved cost counts only genuinely avoided wakes

`gateway_response_cache_wakes_avoided_total{app}` increments
**only** when a hit lands on an app with zero healthy instances
(`backend.HealthyCount(appID) == 0`). A hit against an already-warm
app saves latency but saves no compute — the instance is running
and billing either way — and counting it would overstate savings.

This honesty is what makes the number defensible to a customer
reading their own dashboard. Saved cost is computed in the
reporting layer from `pkg/api/limits.go` plan RAM; the counters are
telemetry-only and do not enter the Stripe/Paddle push. The
`gb_ram_hour` SKU is unchanged — this mirrors the existing posture
where `usage_minutes.tx_bytes` is telemetry, not billing.

The operator outcome counter keeps its historical global shape as
`gateway_response_cache_total{outcome}`. Customer-facing per-app
analytics use the additive
`gateway_response_cache_app_total{app,outcome}` counter, with the
same closed outcome vocabulary. The customer hit rate is strictly
`hit / (hit + miss)`; bypass and stale outcomes remain visible but do
not inflate the numerator or denominator.

### D7 — In-process store, per `gatewayd-internal`

`sync.RWMutex` + LRU + global byte ceiling + per-entry cap,
mirroring `PublicAuthCache`. Invalidation on
`db.NotifyEdgeRuleChanged` (rule edits) and `db.NotifyAppChanged`
(deploys), plus TTL expiry.

Rejected alternative: a shared store (Postgres or disk) for
cross-node hits and restart survival. It would put customer
response bodies on platform durable storage — a materially larger
blast radius for the stateless-contract deviation, plus retention,
encryption and GDPR-deletion scope that the in-memory design avoids
entirely. Per-node hit rates are acceptable because
`gatewayd-public`/`gatewayd-internal` already shard by node.

### D8 — Plan gating follows the `geo`/`throttle` precedent

Gated by per-app count rather than a paid-only kind gate:
Free 0 · Hobby 1 · Pro 5 · Scale 20, enforced by
`Limits.EdgeRulesCachePerApp` inside the existing
`CreateEdgeRuleIfUnderQuota` `FOR UPDATE` lock. `IsPaidOnly()` stays
`{jwt, ip}` — caching is not a security control, so the ADR-091 D21
rationale (don't lock a customer out of a feature they haven't
sized yet) does not apply the same way; Free gets 0 because the
cache consumes shared node RAM, which is the actual scarce resource.

## Consequences

**Positive**

- Read-heavy public routes stop paying a wake per request; the
  saving is measurable, not modelled.
- Latency on cached routes drops from wake-path (<350 ms target) to
  in-process map lookup.
- Reuses 13 kinds' worth of existing machinery — no new config
  surface, no new invalidation channel, no new CLI concept.

**Negative / accepted**

- First customer-visible exception to the stateless contract.
  Bounded by opt-in, in-memory, never-authoritative (above).
- Cached bodies occupy `gatewayd-internal` RSS on the node, bounded
  by the global byte ceiling. This is control-plane RAM, not the
  47,600 MB tenant budget, but it is not free.
- Per-node cache means hit rate scales with node count; a fleet of
  N nodes has N cold caches after a restart.
- Authenticated routes get no benefit in v1. This is the deliberate
  safety trade in D3 and is the most likely source of customer
  follow-up.

**Follow-on work (explicitly out of scope)**

Per-principal / authed caching · cross-node shared cache ·
persistence · purge API (`DELETE /v1/apps/{id}/cache`) ·
`ETag`/`If-None-Match` revalidation · `stale-while-revalidate` ·
surrogate keys / tag-based invalidation · `gregale.yaml` manifest
support (no `edge_rules:` key exists today; `throttle` and `budget`
are both CLI-only, so a manifest surface is a separate decision).

## References

- §4.1.2 edge-rule kinds · §4.7 billing · §12 metrics · §17 G13
  stateless contract
- ADR-005 (snapshots are cache, not truth) — the "never
  authoritative" posture this ADR mirrors
- ADR-089 / ADR-091 (edge-rule surface, kind gating, ordering)
- ADR-104 (authenticated-context carrier) — the hook a future
  per-principal cache would use
