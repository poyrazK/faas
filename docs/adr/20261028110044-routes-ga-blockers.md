# ADR-20261028110044: Routes GA blockers — capability-gated background work, route alerts, hold deadline

- **Status:** accepted
- **Date:** 2026-10-28
- **Related:** ADR-20261023115741 (capability flags), ADR-454 (route health
  canary guard), ADR-844 (default report-mode selectors), ADR-845 (monitor
  automatic rollback), ADR-1198 / ADR-20261020161310 (route insights and their
  alert), ADR-20261020085706 (release approvals and their timeout),
  ADR-749 (notification channels), ADR-123 (alert presets),
  ADR-20261026143210 (route labels)

## Context

A code review of the routes product before GA found three blockers and a set
of smaller gaps.

1. **Background work ignored capability flags.** The routes API checks the
   per-account capability release (ADR-20261023115741), but the work that
   runs without a request did not: the production monitor worker (incidents,
   webhooks and automatic rollback), the canary guard inside `AdvanceCanary`
   and rollout recovery (health seeding, history, notifications, holds), the
   insights evaluator and automatic route checks. An account the operator had
   not released the feature to still got holds, incidents and rollbacks.
2. **Monitor incidents and canary holds reached customers only through app
   webhooks.** An account without a webhook receiver learned that production
   broke a route budget, or that a canary was held, only by reading the API.
3. **An enforce-mode hold never ended.** A canary the guard held on unknown
   evidence — a selector that never sees traffic, a quiet app — stayed split
   between stable and candidate until someone noticed. Selectors were checked
   for shape only, so a typo in a method or path produced exactly that hold.

Smaller gaps: `on_regression: abort` silently degrades to a hold when no
Safe Deploy worker holds the rollout lease; a refused monitor rollback was
always recorded as `rollback_target_ineligible`; monitor automatic rollback
had no end-to-end test; per-gateway route label sets can disagree; and the
public Go SDK had no OpenAPI preview/apply methods.

## Decision

**1. Background route work runs only where its capability is released.**
apid resolves the account's release of `production-route-monitoring`,
`route-health-canary-guard`, `route-insights` and `route-requirements` before
each piece of background work; a failed read counts as unreleased.

- The monitor drain defers an unreleased monitor without evaluating it and
  counts `apid_route_monitor_evaluations_total{outcome="unreleased"}`. It
  opens no incident, sends no webhook and rolls nothing back.
- A canary advance (customer or worker) carries the release in its context
  (`state.WithRouteGuardRelease`). With the guard unreleased, `AdvanceCanary`
  and legacy rollout recovery skip the route health check: no decision,
  history, notification or hold. The same holds for route requirements.
  Default selectors are seeded only when the guard is released, and the
  advance response omits the decisions of unreleased guards.
- The worker's route health recovery returns no candidates for an
  unreleased account; automatic route checks fail the claim as skipped; the
  insights evaluator claims and skips the app, so the rotation moves on.
- Alert presets on a metric whose capability is unreleased are hidden, and
  enabling one is refused like creating the rule.

**2. Two app-scoped alert metrics carry monitor incidents and canary holds
to alert rules and notification channels.**

| Metric | Value | Capability |
|---|---|---|
| `route_monitor_incidents` | The app's monitor incidents open at any time within the window (still open, or closed within it). | `production-route-monitoring` |
| `route_health_holds` | The app's in-flight canaries the enforce-mode guard holds now, plus its canaries the guard aborted within the window. | `route-health-canary-guard` |

Both are notification-only (`action` webhook), app-scoped, with windows 5m,
15m, 1h, 6h and 24h (`alert_rules_route_alerts_chk`). A firing notification
lists up to five incidents or holds with their route, signal and reason. Two
presets (`route_monitor_incidents`, `route_health_holds`, Hobby and up, `gt 0`
over 15m) put them in the dashboard grid. The migration widens the metric
CHECKs online, as ADR-20261018083536's did, and is an expand migration.

**3. A route health hold ends after `RouteHealthHoldDeadline` (24 h).** The
route health notification state's `updated_at` now records when the status
began; a hold keeps the time of its first blocking decision while its
context lasts, also when it turns from unknown to regressed. When the rollout
worker's advance is blocked by the guard and the hold has lasted the
deadline, apid aborts the rollout through the same recovery path as a
timed-out approval, with rule `abort_route_health_timeout`, and restores the
stable deployment. 24 h matches the approval timeout default: a day of
traffic settles unknown evidence, and the `route_health_holds` alert reports
the hold long before.

**4. Saving a guard or monitor warns about selectors no request matched.**
`PUT` route health gate and route monitor (enabled) responses add
`warnings`: each selector whose exact `METHOD /path` label had no request
within the plan's telemetry retention, at most 7 days. The read is bounded by
`RouteSelectorObservationTimeout` (2 s) and is advisory; the CLI prints the
warnings to stderr. A gate saved with `on_regression: abort` also warns when
no Safe Deploy worker holds the rollout lease at that moment, since only that
worker runs the recovery.

**5. Smaller fixes.**

- A refused monitor rollback keeps reason `rollback_target_ineligible` and
  adds `refusal_code`, the checked rollback's problem code.
- `TestE2E_Routes_ProductionMonitorRollsBack` covers the monitor's
  automatic rollback end to end: a healthy release becomes the baseline, a
  failing release opens an incident, and the incident requests a checked
  rollback to the baseline.
- The Go SDK's preview client gains `PreviewAppOpenAPIPolicy` and
  `ApplyAppOpenAPIPolicy`, and the gate and monitor DTOs carry `warnings`.
- The docs state that each gateway keeps its own bounded route label set:
  undeclared routes are learned per gateway, so on a multi-gateway fleet a
  rarely used route can be labeled on one gateway and collapsed into
  `__route_other__` on another. Declared routes keep their labels on every
  gateway; critical routes should be declared.

## Consequences

- Turning a routes capability off for an account stops its background work
  within one worker pass. Existing incidents, holds and history stay
  readable.
- Customers can route monitor incidents and canary holds to email, Slack,
  PagerDuty or any configured channel without a webhook receiver.
- A canary can no longer be held indefinitely by the route health guard;
  the abort is visible in the rollout decision history as
  `abort_route_health_timeout`.
- The hold deadline is enforced on the worker's advance, like the approval
  timeout: with no rollout worker running, nothing advances or aborts.
- A deadline abort leaves the guard's notification state as it was, so
  `route_health_holds` stops counting the canary once it is no longer in
  flight; the abort itself is reported by the rollout decision and the
  deployment events.
