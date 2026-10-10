# ADR-942: Default report-mode route health selectors

- Status: Accepted
- Date: 2026-10-09
- Related: ADR-122, ADR-454, ADR-456, ADR-493, ADR-941
- Amends: ADR-454 ("Default is report mode with no selected routes")

## Context

ADR-454 made route health opt-in: an app starts at revision 0 in report mode
with no selected routes, and an unconfigured canary records no route health
history. Most apps therefore roll out with only aggregate signals, which is
exactly the failure ADR-454 set out to prevent. The API-hosting roadmap
(Phase 4) asks for health-gated progression by default. `gregale routes health
suggest` already ranks a deployment's observed routes by customer reach, but
only a customer who knows to run it benefits.

## Decision

When a canary advance is requested at step 0 for an app whose route health
configuration is still at revision 0, APID seeds the configuration before the
atomic advance:

- Eligibility: the account plan includes request telemetry, and exactly one
  other live deployment serves traffic in the candidate's scope (the same
  stable selection the route health report uses).
- Evidence: retained route customer usage for that stable deployment over the
  last `RouteHealthSeedLookback` (7 days), clamped to plan retention.
- Selection: the shared `routehealth.RankRouteUsage` order (distinct tenants,
  then requests, then method/path), limited to `RouteHealthSeedRoutes` (10)
  exact selectors that pass `routehealth.Validate`. The CLI `suggest` command
  uses the same ranking.
- Write: `SetRouteHealthGate` in **report** mode with expected revision 0, so
  a concurrent customer edit wins. An audit event `route_health.seeded` records
  the candidate, stable deployment, revision and route count.

Seeding never selects enforce mode, latency checks or watched statuses, and
never runs after a customer has saved any configuration. Saving an empty
selector list is therefore the opt-out. Seeding is best effort: every failure
is logged and the advance continues, because report mode cannot hold a
rollout. MemStore keeps its Postgres-only telemetry posture, so it seeds only
when a test supplies usage rows.

## Consequences

- Unconfigured canaries now produce saved route health decisions and
  notifications in report mode from their first advance onward.
- The seeding write increments the revision, which resets the observation
  anchor as for any configuration change. Stage 0 traffic before the first
  advance is not compared.
- Enforcement remains an explicit customer decision through `routes health
  set --mode enforce`.
