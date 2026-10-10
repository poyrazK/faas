# ADR-940: Route advisor — edge-rule suggestions from observed traffic

- Status: Accepted
- Date: 2026-10-10
- Related: ADR-122, ADR-201, ADR-493, ADR-844
- Amends: none

## Context

Gregale has eighteen edge-rule kinds, but a customer has to know which rule a
route needs and size it. Route requirements and policy plans (`routes plan` /
`routes apply`) turn declared intent into rules; nothing turns observed
traffic into rules. Retained request telemetry already records, per route
template, the method, status, latency, cold boot, guest outcome and request-time
consumer identity, which is enough to justify three common rules with evidence.

## Decision

`GET /v1/apps/{slug}/routes/advice` (and `gregale routes advise`) reads
retained telemetry for the app's busiest `RouteAdviceMaxRoutes` (50) routes
across deployments and returns suggestions. It is read-only, uses the normal
read scopes and completed MFA, and the same `DebugTelemetryEnabled` gate and
retention clamp as route analytics. The default window is seven days.

A route needs `RouteAdviceMinRequests` (200) requests in the window. A route
that already has a rule of the suggested kind matching it, enabled or not, is
skipped, so a suggestion applied for review is not suggested again.

- **cache** — GET on a literal path where at least 95% of requests are
  anonymous 2xx and the estimated hit share is at least 20%. The estimate
  buckets anonymous 2xx GET/HEAD requests into cache-lifetime windows: the
  first request of a window fills the cache and the rest are hits; a cold boot
  that is not first in its window is a wake the cache would have avoided. The
  lifetime defaults to the platform cache default (60 s) and is a what-if
  parameter (`cache_max_age`, 1–3600 s). Templated paths are excluded because
  each parameter value is a separate cache key the telemetry cannot see.
- **async** — POST where timeouts (guest timeout or 504) are at least 1% of
  requests and at least 5, or the p95 is at least 10 s. Only offered when the
  plan allows async invocation and the app accepts request invocations.
- **throttle** — one identified consumer sends at least half of a route's
  requests, at least one other consumer exists, and anonymous traffic is at
  most 1%. The per-consumer limit is twice the next busiest consumer's peak
  minute, so no other observed consumer reaches it; if the plan's rate ceiling
  is lower, no suggestion is made. The estimate counts the dominant consumer's
  requests above the limit per minute and must be at least 1% of the route.

Every suggestion carries evidence, a what-if estimate, cautions and one rule
body per hostname (platform hostname plus verified custom domains), always
`enabled=false`. Suggestion IDs hash kind, method and route, so they are stable.
Applying creates the rules through the existing edge-rules API, so validation,
plan quotas and audit are unchanged; the CLI keeps them disabled unless
`--enable` is given.

## Consequences

- Customers get rules sized from their own traffic without learning every rule
  kind first, and nothing changes until they apply and enable a rule.
- Estimates are observed_only upper bounds. The cache estimate cannot see
  Authorization or Cookie headers (which bypass the cache, ADR-122) or query
  strings, and assumes no eviction; cautions say so.
- Throttle suggestions cover consumers identified at request time only;
  anonymous abuse is out of scope because a shared anonymous bucket would also
  limit legitimate callers.
- No new tables, writers or daemons: the advisor is a read in apid over
  `request_telemetry` using the existing `(app_id, received_at)` index.

## Not decided here

Retry suggestions (transport failures are not distinguishable in telemetry
today), route deprecation (covered by route lifecycle review), and suggestions
that change existing rules.
