# ADR 20261124212701 · Edge flag routing

- **Status:** accepted
- **Date:** 2026-11-22
- **Amends:** ADR-20261114150000 (flags usability), ADR-20261115163113 (flag
  experiments), ADR-20261119173702 (subject units)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

A variant flag reaches code only through an SDK. A static site, a page the
code does not branch on, or an app in a language without an SDK cannot serve
a variant, so it cannot run an experiment on it either. The gateway already
sees every request, knows the verified customer, stamps kill lists and
records flag evidence for experiments.

The owner chose before this record:

1. **Variant routing:** a variant flag rewrites a request path at the gateway
   (or, later, picks a deployment); decision headers for code were declined.
2. **Anonymous visitors:** bucketed by a first-party cookie.
3. **Measurement:** everyone is served; experiments count verified customers
   only. An anonymous-visitor unit is later work (§5).
4. **Release:** dark behind a platform flag.

## Decision

### 1. Configuration

A variant flag may carry `edge.routes`: up to `FlagsMaxEdgeRoutesPerFlag`
routes, each a `path` (`/pricing` exact, `/docs/*` the subtree) and, per
variant, an action `{ "rewrite": "/pricing-b" }`. An exact route rewrites the
whole path; a subtree route replaces the prefix (`/docs/a` → `/docs-b/a`).
Variants without an action serve the request unchanged. At most
`FlagsMaxEdgeFlags` flags of an environment route at the edge, no path is
routed by two flags, and paths carry no query, fragment, wildcard (other than
the trailing `/*`) or dot segment. Edge routes live in the published flag
configuration, so history, rollback and `flags_version_conflict` apply; the
runtime bundle SDKs read never carries them, so no SDK changes.

CLI: `gregale flag edge set KEY --path P --variant V --rewrite R`,
`flag edge delete KEY [--path P]`, `flag edge list`.

### 2. Evaluation

gatewayd caches each app environment's edge-routed flags (one lookup, emptied
on `feature_flags_changed`, the kill cache's TTL and retry). For a GET or HEAD
request after every identity, access and challenge gate and before the
response cache, the longest matching route (exact first) selects the flag.
The gateway evaluates it with the shared evaluator (`flags.EvaluateVariantWithContext`):

- a **verified customer** is the customer, exactly as an SDK would bucket it;
- an **anonymous visitor** is evaluated as a customer whose id is a random
  UUID kept in the first-party cookie `__Host-gregale_vid` (HttpOnly,
  Secure, SameSite=Lax, one year), also used as its subject. Percentage and
  weighted rules split visitors; customer, group and cohort targeting never
  match them.

Country and browser family are the same gateway attributes the SDK receives;
cohort conditions do not match at the edge. A killed, disabled or missing flag
(the evaluator's fallback) serves the unrouted path, whatever its default
variant's action. The rewritten path is what the response cache keys on and
what the app sees, so variants never share a cache entry; the response is kept
out of shared caches (Cloudflare) because it depends on who asked.

- **Environment:** production and named environments read their own routes;
  a deployment preview host reads its environment's. A PR preview token is
  not an environment and is never routed.
- **Gates:** the identity, access and challenge gates ran on the client's
  path. A rewrite is served only when the target passes the same declared-route
  policy and matches the same IP, geo, JWT, throttle, validation, limit,
  budget, maintenance, respond, WAF and challenge rules; otherwise the request
  is served unrouted and records no exposure.
- **Cookie:** the visitor cookie belongs to the platform. It is removed from
  the request the app sees (so it never makes a visitor look signed in to the
  response cache) and a newly issued one is never stored in a cached response.
- **Telemetry:** request logs and analytics record the path the app served
  (the rewritten one), so a variant's page views are counted under its own path.

### 3. Evidence and experiments

The decision is recorded as one `used` evidence entry in the request's flag
evidence, merged with what the app reports (the app's entry for the same flag
wins). Request telemetry carries it like SDK evidence, so customer-unit
experiments on a routed flag count verified customers' exposures and their
`track()` conversions unchanged. Anonymous rows have no verified customer and
are not counted.

### 4. Release

The `edge_flags` platform flag (by plan) gates both sides, evaluated for the
project's owner (apid) and the app's owner (gateway), so both read the same
account and plan: off, apid refuses a configuration or rollback that adds or
changes edge routes (403, existing routes keep publishing through other edits)
and the gateway serves every request unchanged.

### 5. Later

- **Deployment action** (`{ "deployment": "<tag>" }`): pinning a variant to a
  live deployment needs the public routing snapshot to carry the edge decision
  (the revision-pin path validates pins there). Not in this record's code.
- **Anonymous-visitor unit:** counting visitors needs a unit across exposures,
  `track()` and analytics that today all require a verified customer.

## Consequences

- Apps without an SDK can serve variants and, for verified customers, run
  experiments on them.
- One extra cached lookup per app environment in gatewayd; requests that do
  not match a route pay a map lookup.
- The visitor cookie is set only on a routed request without a verified
  customer, on the app's own host.
