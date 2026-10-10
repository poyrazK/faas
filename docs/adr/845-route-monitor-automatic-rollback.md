# ADR-845: Opt-in automatic rollback for early production route incidents

- Status: Accepted
- Date: 2026-10-09
- Related: ADR-458, ADR-498, ADR-601, ADR-844
- Amends: ADR-498 ("Monitoring is advisory")

## Context

ADR-498 monitors absolute per-route budgets on the fully serving production
deployment and saves incidents, but only notifies. ADR-458 can abort a canary
on a critical route's error regression, and `--rollback-on-5xx` rolls back on
aggregate 5xx in the first window. A release that breaks one critical route
after promotion, while aggregate errors stay low, is therefore detected and
reported but never reverted. Every incident already saves the last healthy
deployment it replaced (ADR-498 release baseline), and ADR-601 checked
rollbacks already restore an exact pair with fail-closed selection.

## Decision

Add a revisioned `on_violation` monitor setting: `report` (default) or
`rollback`. `rollback` requires at least one route with an error budget.

After each committed evaluation, apid claims the app's active incident under
the monitor's account, app and row locks. Evaluation itself still never writes
deployments. The claim decides once per incident, from the immutable opening
report:

- Not decided: report mode, a stale revision, a closed incident, an incident
  already decided, or an incident whose deployment no longer serves all
  traffic (a rollout or rollback owns it).
- `skipped` with a durable reason: no error-budget route violated
  (`latency_only_violation`); the incident opened more than
  `RouteMonitorRollbackWindow` (30 minutes) after the deployment's latest
  traffic transition (`outside_rollback_window`); or no different healthy
  baseline was saved (`no_healthy_baseline`).
- `claimed`: apid then requests an ADR-601 checked rollback from the incident
  deployment to its saved baseline, through the same path as
  `POST /v1/apps/{slug}/rollback`. That path verifies the target artifact,
  applies the production contract gate, and rejects the request unless the
  incident deployment still serves all traffic with no rollout or other
  rollback in its scope. The claim is then replaced by `requested` with the
  operation ID, or by `skipped` with `rollback_target_ineligible`.

Latency budgets never trigger a rollback, matching ADR-458's error-only
automatic recovery. An apid crash between claim and request leaves the
incident `claimed`, which fails closed: no rollback is requested later. The
audit event `route_monitor.rollback_requested` records the incident, route,
both deployments and the operation. The checked rollback's own lifecycle,
notifications and history apply unchanged.

## Consequences

- Customers can opt one production monitor into reverting a release that
  breaks a critical route soon after promotion, without aggregate signals.
- Incidents expose the decision in `rollback`, and `routes monitor explain`
  prints it. No new webhook event is added; monitors keep
  `routes.monitor.*` and the rollback operation keeps its own status.
- A rollback can only target the deployment the incident saved as healthy,
  never an arbitrary older one.
