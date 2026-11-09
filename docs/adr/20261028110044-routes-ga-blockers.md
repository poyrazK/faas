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

## Amendment 3 (2026-10-29): warm latency and platform refusals

A fourth review found that route evidence charged the candidate for things it
did not cause.

1. **Cold boots biased the latency check.** A scale-to-zero canary with a small
   traffic share cold boots far more often than the stable deployment, and
   schedd keeps only service canaries warm. The guard's p95 included cold boot
   time, so a healthy function canary could look slower than stable, be held
   and then aborted at the hold deadline.
2. **Platform refusals counted as release errors.** A 5xx the gateway returned
   because a Gregale dependency could not admit the request (node admission,
   traffic policy, rate-limit or revocation state unavailable) counted as a
   server error for whichever deployment was selected. A control-plane blip
   during a canary or after a release could hold a canary or roll back a
   production release.
3. **Published alert docs linked to unpublished pages.** `docs/alerts.md` is
   published; its route alert section linked to the unpublished route
   monitoring and route health pages and did not say the features are a
   preview.

**Decision.**

- The canary guard ranks latency only over requests that did not cold boot:
  aggregate evidence, pooled windows, customer cohorts, investigation examples
  and latency diagnostics all use the same warm requests. Each latency window
  reports the count as `latency_requests`, and the 100-request latency minimum
  applies to it. Error counts and rates still include every request. Cold-boot
  latency stays compared by Safe Deploy's circuit breaker
  (`abort_cold_boot_p95`), which compares cold boots with cold boots. The
  production monitor keeps end-to-end latency, cold boots included: it checks
  a budget the customer chose, with no stable deployment to skew against.
- The gateway marks a request telemetry row `platform_refusal` when it answered
  with a 5xx, its traffic decision was `refused`, and the refusal reason is
  `node_admission_unavailable`, `policy_unavailable`, `rate_limit_unavailable`
  or `security_unavailable`. Route health, its customer cohorts, client-error
  signals, investigations and the production monitor skip these rows.
  Deadlines, open circuits, `capacity`, `node_capacity` (a 429 at the app's own
  request limit) and failed wakes or boots still count: a candidate can cause
  each of them. The column is an expand-only migration with a `false` default;
  apid stores the flag only on a 5xx.
- The alerts page names the route alerts as a preview and links neither
  unpublished page until the features are promoted.

**Consequences.**

- Until a node's gatewayd runs this release, its platform refusals still count.
- Saved history and reports from before this amendment have no
  `latency_requests` and are evaluated over all requests, as they were made.
- Route health latency investigations (ADR-497) now show no cold boots and no
  wake boot timings: the requests they explain did not cold boot. Production
  monitor incident diagnostics keep both.
- A route whose traffic is mostly cold boots on either side may lack 100 warm
  requests and stay unknown on latency, which holds an enforced canary until the
  hold deadline. Synthetic probes (ADR-847), which keep the canary warm, make
  that less likely.
- The dashboard has no production route monitoring page yet; that is
  frontend work tracked outside this ADR.

## Amendment 4 (2026-11-02): route labels, open minutes and promotions

A fifth review found evidence that never arrived or was anchored too early.

1. **Route evidence depended on the per-route metrics opt-in.** The gateway
   labeled request telemetry only for apps with `route_metrics_enabled`, an
   opt-in presented as per-route Prometheus series (ADR-093). An app without it,
   for example one created before ADR-093 or one whose owner turned the series
   off, recorded every request as `__route_other__`. Its guard windows were
   always unknown, so an enforced canary was held and then aborted at the hold
   deadline, and its monitor never decided anything.
2. **Selected routes could lose their label.** Each app has 50 labels per
   gateway. A label idle for an hour can be reused, and only declared routes
   (OpenAPI, `gregale.yaml`) are protected. A low-traffic guarded route, the
   case pooled evidence exists for, could fold into `__route_other__` on an app
   with many routes or one that answers every probed path.
3. **Minute collapse held route removal forever.** With
   `FAAS_REQUEST_TELEMETRY_MINUTE_COLLAPSE=1` (ADR-20261101085552), the
   gateway reported every open minute's requests as pending. Every app with
   traffic kept a per-app pending gap refreshed on each heartbeat, so route
   removal's coverage check always blocked it.
4. **Manual promotions never anchored the monitor.** A traffic change that
   took a deployment to 100%, such as promoting a release staged with
   `--no-traffic`, recorded no time. The monitor anchored on the deployment's
   creation, so an error-budget incident right after the promotion was skipped
   as `outside_rollback_window`.

**Decision.**

- Request telemetry labels routes for every app with request telemetry,
  whatever its per-route metrics setting; per-route Prometheus series still
  follow the opt-in.
- The gateway reserves labels for the routes an app's route health guard or
  production monitor selects (at most 40). Such a label is never replaced, and
  a full set with no idle label gives up its least recently seen unselected
  label. gatewayd-internal reads the selections (`RouteLabelPins`) and caches
  them for a minute; a failed read keeps the last known selections and retries
  after 10 seconds. Selections never change how a request is handled.
- Coverage counts as pending only requests in closed minutes that have not
  shipped. A minute still within its grace is read by no window yet: route
  evidence waits `RouteHealthIngestionLag` and route removal two minutes, and
  each tick ships closed minutes before it reports coverage.
- `deployments.promoted_at` records when a traffic change last took a
  deployment to 100%, from below (manual splits, binding promotions and staged
  promotions all use that path). The monitor's observation anchor and its
  rollback window include it.

**Consequences.**

- Apps without per-route metrics now write one telemetry row per route
  bucket instead of one `__route_other__` bucket; the 50-label cap still
  bounds them.
- A selected route can displace a busy unselected route's label; that route's
  requests then count under `__route_other__` and its Prometheus series end.
- A gateway that fails before shipping an open minute loses it as before; the
  restart resets its coverage, so route evidence and removal still see the gap.
- Releases promoted before this amendment have no `promoted_at`; the monitor
  keeps anchoring them on their recorded rollout times.

## Amendment 5 (2026-11-02): named deployments, long-lived responses, pin reads

A sixth review found traffic that route evidence should not count and a pin
cache on the request path.

1. **Requests that named their deployment counted as production evidence.**
   Every deployment has a preview URL (`https://deploy-44-my-api.gregale.dev`),
   deployments can have alias URLs, and project releases accept revision pins.
   Their requests were ordinary telemetry for that deployment. CI smoke tests,
   QA sessions and negative tests against a canary's URL counted as canary
   evidence, and the same against the serving deployment's URL could open a
   production incident and, with automatic rollback on, roll back a healthy
   release.
2. **Long-lived responses counted their lifetime as latency.** A request's
   latency runs until the handler returns, so a WebSocket or a declared stream
   recorded its whole connection. A latency budget or relative check on such a
   route could never pass.
3. **Pin reads blocked requests.** Amendment 4's pin cache took one mutex on
   every request and read the store inside the request that found an entry
   expired, with a 500 ms timeout.

**Decision.**

- Request telemetry records `deployment_pinned` when the gateway served a
  deployment preview or alias URL (`PinnedDeploymentID` without a
  `PublicEnvironmentID`) or a revision pin, and `long_lived` when the response
  outlived its request budget (`streamDetached`: a WebSocket upgrade or a
  declared stream). An environment URL, a project release and production
  routing are not pinned.
- Route health, its customer cohorts, client-error signals, investigations and
  the production monitor skip pinned requests. Every reader counts a
  long-lived response as a request and its errors, but leaves its duration out
  of latency; a side reports `latency_requests` whenever it ranked fewer
  requests than it counted, and the latency minimum applies to that count.
- A lookup only reads the pin cache. An app's first lookup and each expiry
  start one background read; lookups keep the last known pins meanwhile.

**Consequences.**

- A guarded route's evidence no longer includes traffic sent to the
  deployment's own URL; a route used only through such URLs has no evidence.
- A route that only serves long-lived responses has no latency evidence and
  stays unknown on latency checks.
- Until a node's gatewayd runs this release, its rows carry neither flag.
- An app's first request after a gateway starts can still fold a selected
  route into `__route_other__` while its pins load.

## Amendment 6 (2026-11-04): removal evidence labels, contract reads, clone state

A seventh review found route removal evidence that could miss a removed
route's traffic, a contract read on the request path, and route monitor state
registered as clone configuration.

1. **Removal evidence matched one label.** Approval and the cutover gate
   (`route_removal_check`) counted a removed operation only when telemetry
   carried its exact method and contract path. Once the app's current OpenAPI
   document no longer declares the route (the customer uploads the candidate's
   contract before approval), the gateway labels its requests with the
   inferred form (`{id}` only for numeric, UUID or hex segments) or the raw
   path, or folds them into `__route_other__` when the app's 50 labels are
   taken. HEAD requests to a GET route carry `HEAD`, and contract paths with a
   trailing slash never matched. Removal could be approved while customers
   still called the route.
2. **The declared-route document was read on the request path.** When an
   app's cached document expired (every 5 minutes), the request that found it
   expired, and every concurrent one, read it from Postgres under the request's
   own context with no bound, and a failed read was returned to the request.
   Apps that only allow declared routes answered 503; other apps silently
   labeled the request with the inferred form, splitting route health and
   monitor evidence.
3. **`route_monitors` registers runtime state as configuration.** Its clone
   registry entry lists `active_incident_id`, `last_deployment_id`,
   `last_healthy_deployment`, `customer_recovery_state` and `next_check_at`.
   Route configuration has no clone capability yet; when it gains one, copying
   those columns would point a clone at the source's incident and deployments.

**Decision.**

- `route_removal_route_observed` (migration `20261108095332288`) is the one
  evidence check for approval and the cutover gate. A removed operation
  matches telemetry whose route fits its template (each `{parameter}` one
  segment, optional method prefix, optional trailing slash) for its method,
  with HEAD counted for GET. Rows labeled with an operation the candidate keeps
  are not the removed route.
- The gateway labels a request its current contract does not declare with the
  template of the app's route removal baseline contract, as a declared label
  that never folds. The baseline only labels; it never admits a request. It
  loads in the background and never on the request path.
- The declared-route cache serves an expired document while one background
  read renews it. Every read is bounded at 2 s and shared by concurrent
  requests; a failed renewal keeps the document and retries after 10 s. A
  document the store has not verified for 30 minutes past its TTL is no longer
  served, so the next request reads and fails closed. Invalidation still drops
  the document at once.
- `route_monitors` gains clone capabilities only once a clone starts without
  the source's runtime state; a test enforces this.

**Consequences.**

- Approval and cutover can block on traffic to a different route of the same
  shape that the candidate does not declare; declaring that route clears it.
- Until a gateway learns the baseline (at most one TTL), a removed route can
  still fold into `__route_other__` on a full label set; the pattern match
  covers every other label.
- An app that only allows declared routes keeps its last verified document
  for up to 30 minutes when the store cannot be read and no change
  notification arrives.
