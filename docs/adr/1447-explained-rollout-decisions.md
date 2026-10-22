# ADR-1447 · Explained rollout decisions

- **Status:** proposed; n2 drill passed 2026-10-14 (see Rollout)
- **Date:** 2026-10-14
- **Amends:** [ADR-1446](1446-safe-releases-by-default.md) (rollback outcome notifications were a follow-up), [ADR-224](224-account-scoped-release-webhooks.md) release payloads
- **Follows:** [ADR-122](122-safe-releases.md) deployment audit, [ADR-456](456-saved-canary-route-health-decisions.md) saved route health decisions
- **Decision:** Record the evidence behind every decision the safe-release circuit breaker makes about a rollout: why a stage advanced, why it is held, and why it was aborted. Show it on the deployment, in the deployment audit timeline, in `gregale deployment wait --rollout` and `deployment summary`, and in the `rollout.completed` and `rollout.aborted` webhooks.
- **Why:** ADR-1446 made a health-gated canary the default for production releases, but its decisions reach only meterd's log and a metric. A customer whose release sits at 1% sees no reason; an aborted release says only "5xx error rate regression". On production-us, `deployment wait --rollout` printed nothing for ten minutes while meterd held a canary for too few requests. A safety default that cannot explain itself is one customers turn off.
- **Rejected alternatives:** Writing every hold onto the deployment row (triggers such as `binding_promotion_revision` fire on any update of `deployments`); one audit row per 30 s tick (unbounded noise); a second history table like ADR-456's `route_health_history` (the deployment audit timeline already exists, is customer-readable, and is what the dashboard renders).

## Context

meterd's canary progression evaluates the candidate against its serving predecessor every tick (`pkg/canary.EvaluateCircuitBreaker`). It knows the request and probe counts, 5xx, p95 and cold-boot latency, CPU per request, dependency errors, OOM kills and liveness restarts for both sides, and which rule decided. It then calls APID's loopback-only Safe Deploy routes with only `expected_step` (advance) or a category string (abort). APID writes a `deployment_audit` row with `{from_percent, to_percent}` or `{action, reason}`. Holds are not recorded at all. ADR-456 saves evidence only for customer-selected critical routes; a default rollout has none.

## Decision

### The decision record

`api.RolloutDecision` is a small, aggregate-only object:

- `kind`: `advance`, `hold`, or `abort`.
- `rule`: a closed set that reuses the `canary_progression_circuit_breaker_total` event names: `advance_healthy`, `advance_probe_evidence`, `advance_low_traffic`, `advance_no_predecessor`, `advance_manual`, `hold_insufficient_samples`, `hold_signal_unavailable`, `hold_observation_unavailable`, `hold_health_alert`, `abort_5xx`, `abort_p95_latency`, `abort_cold_boot_p95`, `abort_cpu_per_request`, `abort_dependency_errors`, `abort_oom`, `abort_crash_loop`, `abort_manual`, `abort_worker_lease_expired`.
- `summary`: the breaker's reason text, which contains only metric names and aggregates.
- the evaluated step, the candidate's traffic percent, the observation window, the stable deployment ID, and per-side samples (requests, 5xx, probe requests and probe 5xx, p95, cold boots and their p95, CPU requests and CPU µs, dependency calls and errors), OOM kills, and liveness restarts.
- `probes_included` when synthetic probes were part of the sample, and `decided_at`.

No request payloads, routes, URLs, trace IDs, or free text from customers enter the record. APID validates the closed sets, non-negative numbers, timestamps, and a 2 KiB encoded bound (`api.RolloutDecisionMaxBytes`, summary at most `api.RolloutDecisionSummaryMaxBytes`) and rejects anything else.

### Who may supply it

Only meterd's Safe Deploy routes accept a client-supplied decision: the canary credential on advance and on the new hold route, the action credential on exact recovery. Customer requests never carry one. APID builds the record itself for transitions it performs on a person's behalf (`advance_manual`, `abort_manual`) and for the worker lease expiry abort (`abort_worker_lease_expired`).

### Where it is stored

- **Advances and aborts** write the record to a new nullable `deployments.rollout_decision` column in the same `UPDATE` as the traffic transition, and into the `data.decision` of the `deployment_audit` row written in the same transaction. A recovery transition that has no record clears the column, so it is never stale for the transition that wrote it.
- **Holds** are written by a new loopback route, `POST /v1/internal/safe-deploy/deployments/{id}/canary/hold`, as `deploy.rollout_held` audit rows. A hold row is inserted only when the deployment is still on the evaluated step and either the step has no hold row with the same rule or the newest one is at least `api.RolloutHoldRefreshInterval` (5 minutes) old. A held canary therefore adds at most one row per rule per five minutes, and holds never write the deployment row.

The audit timeline keeps its existing 90-day retention.

### Where it is shown

- `DeploymentResponse.rollout_decision` returns the stored record. `GET /v1/deployments/{id}` and the deployment summary also return `rollout_hold`: the newest hold row recorded since the current step started, while the rollout is in flight.
- `GET /v1/deployments/{id}/audit` returns the records inside each row's data.
- `gregale deployment wait --rollout` prints the recorded hold reason instead of a generic hint, and prints each advance and the abort evidence. `deployment summary` prints the latest decision and current hold.
- The `rollout.completed` and `rollout.aborted` webhook payloads carry `decision` when the stored record explains that outcome: an `advance` record for completion, an `abort` record for an abort. An abort by another path (route monitor, alert rollback, profile gate) keeps its existing `reason` and no `decision`, so an earlier advance record is never presented as the cause.

## Consequences

- A customer can see why a release is waiting, what it is waiting for, and the numbers behind every promotion and rollback, without asking an operator to read meterd's log.
- Webhook receivers get the evidence with the event. No event type is added, so account-scoped filters (ADR-224) are unchanged.
- meterd sends one extra loopback request per held canary per tick; APID writes at most one row per rule per five minutes.
- The record explains only the circuit breaker's decisions and the manual and lease-expiry transitions above. Route gates and route health keep ADR-456's history; profile gates and mirror conditions keep their existing responses.

## Rollout

Ship with the record written and shown everywhere at once. The safe-release drill asserts that a quiet canary's completion carries an `advance_probe_evidence` record with probe counts for both revisions, that a probe-backed abort carries an `abort_5xx` record with `probes_included`, that the bad release carries `abort_5xx` with the candidate's 5xx, and that the crash loop carries `abort_crash_loop` with its restarts. It logs the quiet canary's hold rows; whether a hold occurs depends on how many probes arrived by the stage boundary.

The drill passed on the n2 node on 2026-10-14 (all six cases, leakcheck clean): the quiet canary completed on `advance_probe_evidence` with 20 probes on each side at the decision and no hold at the boundary; the late-failing release aborted on `abort_5xx` with `probes_included` and candidate probe 5xx 20/20 ("5xx regression (candidate 95.2%, stable 0.0%; 20/21 vs 0/21 requests; synthetic probes included)"); the bad release aborted on `abort_5xx` and the crash loop on `abort_crash_loop`. The same run's Postgres test caught the hold insert's untyped time parameter, which made every hold fail; holds are covered by `TestRolloutDecisionsPG`.

## Amendment (2026-10-22): request evidence for decisions

The record says why the breaker decided, in aggregates; a customer whose
release was rolled back on `abort_5xx` still has to find which requests
failed. Two debugger reads (ADR-127) now join a decision to the request
telemetry behind it, at request time. Nothing is added to the record, which
stays aggregate-only, and both reads sit behind the debugger's plan gate and
retention.

- `GET /v1/apps/{slug}/debug/rollout` (`gregale debug rollout <slug>`)
  explains the newest of the app's last 20 deployments with a canary rollout,
  or `?deployment_id`.
  - It returns `state` (`aborted`, `held`, `rolling_out`, `complete`, `none`)
    and the record that explains it: the abort record, the newest hold, or the
    latest advance.
  - An abort from another path (route monitor, alert rollback, profile gate)
    has no record; `aborted_reason` carries that path's reason.
  - The window is the decision's observation window. A hold is shown up to
    now, since the canary is still waiting. Without a window, the current step
    (or the whole rollout) up to the abort or completion is used.
  - Over that window, the candidate's routes are compared with the stable
    deployment (the record's `stable_deployment_id`, otherwise the other
    serving deployment with the most traffic). Routes are ranked worst first:
    by p95 increase for a latency rule, otherwise by server-error rate
    increase. At most five are returned.
  - Up to three example requests come from the worst route: the newest server
    errors, or the newest requests at or above the route's p95 for a latency
    rule. Each links to its request evidence.
  - The candidate's open regressions are included, and a deterministic
    headline names the decision and the worst route.
- `GET /v1/apps/{slug}/debug/status` (`gregale debug status <slug>`)
  summarizes the app over `since` (default 1h) as one verdict, worst first:
  - `failing`: at least 5% server errors over at least 20 requests;
  - `rolled_back`: a rollback inside the window;
  - `degraded`: an out-of-memory kill, open regressions, or any server
    error;
  - `releasing`: a rollout in flight;
  - otherwise `healthy`, or `idle` with no requests.

  It also returns the serving deployments, their requests, server errors and
  p95, the rollout explanation when one is in flight or rolled back in the
  window, the open regressions, the five newest server-error requests, the
  five newest out-of-memory kills with their total and the remedy for the
  newest (the `app_runtime_oom` fix text for its peak and plan), and whether
  tracing is on. The CLI prints the drill-down commands.

The logic lives in `pkg/apid/rolloutevidence` (ADR-982). It reads existing
sqlc queries, `RequestTelemetryErrorsByRoute`,
`RequestTelemetryBaselineP95ByRoute`, `ListRequestTelemetryByApp` and
`RequestTelemetryAnalyticsSummary`, plus `ListAppWorkloadOOMKills`, which
reads the app's `instances.workload_oom_failed` events (ADR-121) through the
existing `events_kind_at_idx`. No migration or index is added.
