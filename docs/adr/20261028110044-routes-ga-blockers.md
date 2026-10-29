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

## Amendment 1 (2026-10-29): production lane and lost telemetry

A second review found two more gaps.

1. **The production monitor watched only the default scope.** Project
   releases (multi-service projects, GA since ADR-20261027120000) serve from
   the named `production` scope, so a project app's monitor never found its
   serving deployment: no incident, alert or automatic rollback.
2. **Lost telemetry looked like quiet traffic.** Route evidence counted only
   the rows that reached `request_telemetry`. A gateway restart, a heartbeat
   gap or rows a gateway could not attribute leave a window short of rows,
   which compared as sparse or, worse, as a skewed rate. With the 24 h hold
   deadline, a hold the platform caused could end the release.

**Decision.**

- The stored monitor (and so incidents, `route_monitor_incidents` and
  automatic rollback) selects the app's production routing lane, the rule of
  `state.ProductionRoutingDeployments`: the named `production` scope once it
  carries traffic or while the default lane is dark, otherwise the default
  scope. Other environments keep their read-only scoped report.
- A project release rolls back as a set, so an eligible incident on a
  `production`-scope deployment is skipped with reason `project_release`
  instead of rolling one member back. The app canary guard and its probes stay
  default-scope: project releases verify themselves (ADR-20261019120004).
- Route evidence reads gateway coverage (`request_telemetry_coverage`, the
  heartbeats route removal already uses). An active node counts as having lost
  telemetry for windows `[from, until)` when its telemetry is off or sampled,
  when it has not been continuously healthy since `from` (a restart, a
  heartbeat gap, or rows it could not attribute to an app), or when its last
  report predates `until`. Rows a gateway attributes to an app and drops are
  excluded: they include an account over its plan's telemetry rate, whose
  remaining rows stay comparable, and holding such apps would never end.
- Lost evidence makes the canary guard's windows unknown with the new window
  reason `telemetry_incomplete`. The advance waits, but the hold is the
  platform's: no `routes.health.blocked` webhook, no notification state, not
  counted by `route_health_holds`, and the hold deadline never aborts on it
  (`RouteHealthBlockedError.EvidenceLost`). The monitor reports
  `telemetry_unavailable`, a report reason every client already accepts; no
  incident opens or recovers and no healthy baseline is recorded.
- No operator alert is added: the gateway's dropped-rows counter also counts
  plan-rate drops, which are expected, and the windows recover within minutes
  of a restart.

**Consequences.** After each gateway restart, canary advances and monitor
evaluations wait about two minutes for complete windows. Pooled evidence over
the last 30 minutes can still include a gap older than the current windows;
it is read as before. An apid outage is detected: gateways send coverage
heartbeats over the same apid RPC as the rows, so an outage resets every
gateway's healthy-since time (corrected by amendment 2).

## Amendment 2 (2026-10-29): stopped gateways, aborted holds, customer grouping

A third review found these gaps, two of them in this ADR's own changes.

1. **One stopped gateway held every app's evidence.** Amendment 1 counted an
   active node silent since before the window's end as lost telemetry. A
   live node whose gateway crashed, stopped publishing or was removed keeps a
   stale coverage row while schedd still sees the node, so every app's route
   evidence stayed unavailable: enforced canaries neither advanced nor timed
   out.
2. **A deadline abort read as a resolved hold.** The abort left the guard's
   notification state blocked, so the aborted canary dropped out of
   `route_health_holds` (no longer in flight, not `aborted`): the alert sent a
   resolve notification as the release was aborted, and no
   `routes.health.aborted` webhook went out.
3. **Per-customer evaluation relied on internal identities.**
   `customer_group_by` (monitor, monitor preview, route health customers,
   investigations) groups by platform tenants or API consumers, both internal
   capabilities, with no gate.
4. **Holds had no visible deadline.** Nothing said when a held canary would be
   aborted.

**Decision.**

- A gateway whose last coverage report is more than
  `RouteTelemetrySilenceGrace` (30 s) older than the evidence windows' start is
  ignored, whatever that report said. A gateway that fell silent within the
  grace or inside the windows still makes them `telemetry_incomplete`. One
  stopped gateway now costs the evidence windows it overlapped, not every app's
  evidence until an operator acts. A gateway that keeps serving while it stops
  publishing goes unnoticed by route evidence.
- After a hold-deadline abort, apid records the hold as `aborted`
  (`RouteHealthHoldAborter`), so `route_health_holds` counts it for its
  window, and app webhooks receive `routes.health.aborted` with reason
  `hold_deadline_exceeded`. That webhook names the hold's first blocking
  decision, whose saved evidence explains the hold. If bounded history has
  pruned that decision, the abort is recorded without a webhook.
- `customer_group_by tenant` needs `platform-tenants` and `consumer` needs
  `api-consumers` released for the account; otherwise the request is refused
  with `capability_not_released`.
- A route health report read while the enforced guard holds the canary adds
  `hold.since` and `hold.aborts_at`. The CLI prints them.
