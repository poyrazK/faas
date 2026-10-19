# ADR-1199: Route evidence — check cache rules from refills, credit probes and prewarm

- Status: Accepted
- Date: 2026-10-16
- Related: ADR-1188 (learned cacheability), ADR-1187 (route awake report), ADR-1198 (route insights), ADR-847 (synthetic route probes), ADR-160 (scheduled prewarm), ADR-122 (edge response cache)
- Amends: ADR-1188 ("validating the max-age of existing cache rules" was not decided), ADR-1187 (probes and prewarm were unattributed)

## Context

Two gaps left the route evidence incomplete.

- Learned cacheability observes anonymous GET bodies only on routes without
  a cache rule. A route that already has one is never checked: a rule whose
  content changes faster than its max-age serves stale responses unnoticed,
  and a rule much shorter than the content's lifetime refills, and can wake
  the app, more often than it needs to.
- The awake report leaves every awake minute that no customer request
  explains "unattributed", including the time Gregale itself keeps an app
  awake: synthetic canary probes, which by design write no request telemetry
  (ADR-847), and scheduled prewarm.

## Decision

**Observe refills.** On a cache rule's miss path, gatewayd-internal feeds the
body the cache is about to store into the same per-URL observer as
ADR-1188 (keyed also by the rule's vary hash). The cache writer has already
buffered the body, so nothing new is held. The telemetry outcome is:

- `stale`, a new value: the body changed, and the refill came within the
  rule's max-age plus `RouteContentStaleLagShare` of it (at least
  `RouteContentStaleMinLag`). The change almost certainly happened while the
  previous copy was still being served.
- `same_<w>` bucketed by how long the body has stayed identical across
  refills (each refill matched the previous one), the evidence that a longer
  max-age would be safe.
- `changed_<w>` by the gap otherwise, as in ADR-1188; `uncacheable` when the
  cache will not store the response.

A migration adds `stale` to the `request_telemetry.content_outcome` CHECK,
re-added `NOT VALID`; a second migration validates it in its own
transaction, so the scan does not block inserts.

**Check rules.** The route advisor's statistics count stale refills. For the
enabled cache rule the gateway would apply to a GET route (lowest priority
first), a rule is:

- stale when at least `RouteCacheRuleMinRefills` refills were observed and
  more than `RouteCacheRuleMaxStaleShare` were stale;
- too short when none was stale and the measured safe max-age (ADR-1188's
  verdict over the refill evidence) is at least `RouteCacheRuleTooShortFactor`
  times the rule's.

The route insights evaluator (ADR-1198) records these as two new insight
kinds, `cache_stale` and `cache_too_short`, delivered like the others.

**Credit probes and prewarm.** The awake report reads, per minute, probes the
app answered without a 5xx or an auth rejection (`route_probe_observations`)
and prewarms that admitted instances (`prewarm_intents`). An awake minute no
customer request explains is credited to the latest such event that still
keeps the app awake; customer requests always come first. A route's what-if
treats probe and prewarm minutes as cover, so removing a route no longer
claims minutes a prewarm would have kept awake anyway. The report lists the
sources and the app's current minimum instance count. Probes stay out of
request telemetry, as ADR-847 decided.

## Consequences

- Customers learn when a cache rule serves stale content or refills far too
  often, without running anything.
- Refill evidence is per gateway like ADR-1188's: with a shared cache, a
  gateway compares only the refills it performed. Rules longer than an hour
  are never judged too short, since an hour is the longest measured window.
- Probe observations are pruned after a day, so probes older than that stay
  unattributed. Prewarm attribution uses the fire time, not each instance's
  own wake.
- Telemetry gains one outcome value; every reader that does not count it is
  unaffected.

## Not decided here

Suggesting a purge-on-deploy setting for stale rules, judging rules with
`vary_on` per variant, and attributing cron, queue and `waitUntil` work.
