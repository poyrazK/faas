# ADR-1152: Route feature evidence, advisor settings, and token-free rollout completion

- Status: Accepted
- Date: 2026-10-11
- Related: ADR-122, ADR-976, ADR-977, ADR-978, ADR-982
- Amends: ADR-977 (wake-ahead metrics), ADR-978 (priority queue metric), ADR-122 (safedeploy tick gating)

## Context

Service wake-ahead (ADR-977) and route priority (ADR-978) are opt-in and act
only under specific conditions: a cold chain, a saturated app. Their effect
was visible only as fleet-wide Prometheus counters, so a customer could not
tell whether either feature would help, or was helping, their app. The route
advisor (ADR-976) suggested edge rules but not these settings.

Separately, #1926: meterd built the safedeploy orchestrator only when
`FAAS_SAFEDEPLOY_TOKEN` was set, and no installation path ever sets it. Every
deployment therefore stayed `rollout_state: pending`, including non-canary
deployments the state machine should complete on its first tick.

## Decision

**Evidence.** gatewayd-internal publishes per-app evidence as Prometheus
series, bounded by construction:

- `gateway_service_wake_ahead_edge_share{caller_app,target_app,kind}` and
  `gateway_service_wake_ahead_caller_wakes{caller_app}`: the learner's top
  `ServiceWakeAheadMaxTargets` edges per caller, published only once the caller
  has `ServiceWakeAheadMinWakes` wakes, withdrawn when an edge leaves the top
  or the caller is evicted.
- `gateway_service_wake_ahead_edge_total{caller_app,target_app,outcome}`: only
  for edges that were actually woken ahead (or skipped for residency).
- `gateway_route_priority_queue_total` gains an `app` label; only apps whose
  requests had to queue produce series.

apid reads the fleet aggregate (max across gateways for learned shares, sums
over `RouteEvidenceWindow` = 24 h for outcomes), the same way observed routes
are read, in the `pkg/apid/routeevidence` domain package (ADR-982), and returns it on `GET /v1/apps/{slug}/service-wake-ahead` (`learned`)
and `GET /v1/apps/{slug}/route-priorities` (`queue`). Wake-ahead targets outside
the caller's account are never shown. Without Prometheus the blocks report
`source: unavailable`.

**Advisor settings.** The route advisor adds two app-level suggestion kinds
that carry a `setting` instead of edge rules:

- `wake_ahead` when the app is not opted in and at least one measured edge
  meets the wake-ahead thresholds;
- `priority` when the app has no priorities (not even the route-health
  default) and its queue turned away or displaced at least
  `RouteAdviceMinShed` requests in the window. It proposes the busiest write
  routes (up to `RouteAdviceMaxCriticalRoutes`) as critical, for review.

Applying either goes through the setting's own API. `gregale routes status`
marks routes with a pending suggestion (`advice_available`) and counts all
suggestions.

**Rollout completion without tokens.** meterd always builds the safedeploy
orchestrator. Without the safe-release tokens it runs in completion-only mode:
a live deployment with no ladder, or a ladder already at its last step,
becomes `complete` with `rollout_completed_at`; no ladder is started, no stuck
detection runs, no APID recovery or alert actions are wired. With both tokens
the behaviour is unchanged. Failed and cancelled deployments are already
fenced to `aborted` by the schema, and every reader of pending rollouts
filters on `status = 'live'`.

## Consequences

- Customers can see what wake-ahead measured and achieved, and how priority
  shaped the queue, before and after opting in; the advisor turns that
  evidence into a one-step proposal.
- Evidence is per gateway and in memory; after a gateway restart the learned
  shares rebuild, and outcome counts cover only the window.
- Deployments stop reporting `pending` forever on installations without the
  tokens. Canary ladders still need the tokens to advance.

## Not decided here

Showing per-route queue outcomes (the queue metric has no route label by
design) and persisting learned edges.
