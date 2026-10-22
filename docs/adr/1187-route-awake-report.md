# ADR-1187: Route awake report — which routes keep a scale-to-zero app awake

- Status: Accepted
- Date: 2026-10-13
- Related: ADR-127 (request telemetry), ADR-976 (route advisor), ADR-122/214 (edge response cache), ADR-847 (synthetic route probes), ADR-982 (apid packages)

Amended by ADR-1199: awake minutes no customer request explains are credited
to synthetic route probes and scheduled prewarm, which also count as cover in
each route's what-if.

## Context

On a scale-to-zero platform a route's cost is mostly the awake time it
causes, not its CPU. One uptime monitor calling `GET /health` every minute
keeps an app with a 60 s idle timeout awake all day: it is billed for every
running second (plan RAM + 8 MB, spec §4.7), it never parks, and the node
loses the density that parking buys. Customers see the bill and the
`gateway_wake_latency_seconds` they avoid, but nothing tells them which
routes keep the app awake or what removing that traffic would change.

The data exists: `usage_minutes` holds the billed MB-seconds and cold boots
per instance and minute, and `request_telemetry` holds per-minute request
counts per route with the status, deployment and cold-boot flag.

## Decision

Add a read-only report, `GET /v1/apps/{slug}/routes/awake` and
`gregale routes awake`, behind the same plan gate, scopes, MFA and retention
window as the route advisor.

**Awake time** is the app's billed running time per minute from
`usage_minutes`, summed over instances.

**Requests that keep the app awake** are those that restart the idle timer:
a 2xx response from an instance (the gateway only touches `lastSeen` on
2xx), or a request that cold-booted it. Requests the gateway answered itself
(edge cache hits, edge answers, rejected credentials) carry no deployment,
instance or wake and are not counted. The busiest `RouteAwakeMaxRoutes`
routes are reported; the rest form one "other routes" bucket.

**Attribution.** Walking the window minute by minute, each awake minute is
credited to the requests of the latest minute that still keeps the app
awake (within the idle timeout plus one minute of bucketing slack), split
by request count. Awake minutes no request explains are *unattributed*:
synthetic probes (not in telemetry by design), scheduled prewarm, minimum
instances, queue, cron and `waitUntil` work, or telemetry dropped by the
plan's rate cap.

**What-if.** For each reported route, the report recomputes which awake
minutes would still be covered had that route's requests never reached the
app, with every other request unchanged. Credited minutes that lose cover
are the awake time saved; the change in the number of awake stretches is the
change in cold wakes, which is positive when the route was bridging gaps
between other traffic. Unattributed minutes are never counted as saved.

**Patterns and suggestions.** A route is *periodic* when it was called in at
least `RouteAwakePeriodicMinMinutes` distinct minutes and both the gaps
between those minutes and the requests per minute hold steady
(`RouteAwakePeriodicMinShare`). For a periodic route whose removal would
save at least `RouteAwakeSuggestMinSavedShare` of the app's awake time:

- anonymous GET/HEAD traffic (`RouteAwakeCacheMinAnonymousShare`) gets an
  `edge_cache` suggestion: a cached response never wakes the app, and
  `gregale routes advise` drafts the rule;
- other traffic called more often than the idle timeout gets a
  `call_less_often` suggestion.

The report computes in `pkg/apid/routeawake` (pure analysis plus a narrow
store interface); apidserver only wires the handler. MemStore has no
telemetry and answers 503, as for the route advisor.

## Consequences

- Customers can see why an app never parks and what a change would save
  before making it. The platform benefits too: every app that parks frees
  its RAM for others.
- Every figure is an estimate at minute resolution. Telemetry rows are
  collapsed per minute (ADR-127), the multi-instance model treats the app's
  instances as one timeline, and awake seconds are instance-seconds.
- The report is read-only; it never changes rules, timeouts or billing.

## Not decided here

Suggesting idle-timeout changes, feeding the what-if into the route advisor
as a ranking signal, and learning arrival rhythms to wake ahead of predicted
traffic.
