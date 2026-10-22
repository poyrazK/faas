# ADR-20261022082305 · Synthetic and relative release gates

- **Status:** proposed
- **Date:** 2026-10-22
- **Amends:** [ADR-20261019071734](20261019071734-release-gates-on-customer-signals.md) (release gates)
- **Uses:** synthetic checks ([ADR-748](748-synthetic-checks.md)), route probes ([ADR-847](847-synthetic-route-probes.md), [ADR-1449](1449-app-chosen-rollout-probe-routes.md)), custom metrics ([ADR-202](202-custom-application-metrics.md))
- **Decision:** Two more release gate kinds.
  - `synthetic` names one of the app's synthetic checks. While a canary rolls out, apid sends that check's request to the candidate and to the stable revision every minute, pinned to each revision through the route-probe challenge. The gate fails when the candidate fails more of its probes than the gate allows and more than the stable revision does.
  - `metric_change` bounds how far a custom metric may move. It compares the metric's average over the release's window with its average over the same length of time before the rollout started. The gate fails when the metric rose (`above`) or fell (`below`) by more than `max_change_percent`.
- **Why:** The first gate kinds judge traffic the release happens to receive and a custom metric against a fixed number.
  - **Synthetic checks.** A checkout path the canary rarely serves can break without the SLO or the error capture noticing. The app usually already has a synthetic check for that path, but checks run every 5 to 60 minutes against whatever serves the public hostname. They cannot tell the candidate from the stable revision, and they report after the release is done.
  - **Relative metric bounds.** A fixed threshold has to be guessed and goes stale as the business changes. "Orders per minute must not drop by more than 30%" holds across seasons where "orders per minute must stay above 40" does not.
- **Rejected alternatives:**
  - **Judging the synthetic check's scheduled runs.** They are too sparse for a canary that finishes in minutes, and they hit the public hostname, so a run during a 5% canary almost always measures the stable revision.
  - **Counting probe timeouts as failures.** A probe that gets no answer carries no proof from the gateway of which revision it reached. A cold boot of a scale-to-zero candidate can also outlast a probe. Like route probes, an unattributed probe is not counted. The canary's own latency and crash rules judge a release that hangs.
  - **Judging synthetic gates in the post-release watch.** Once the rollout completes there is no serving predecessor to probe beside the release. A shared dependency outage would then roll back a good release. The canary has already probed the release at every stage.
  - **A relative bound on the per-revision metric.** Custom metrics are reported per app, not per deployment (ADR-20261019071734). The comparison is across time, so the gate is strongest in the post-release watch, where the release takes all the traffic.

## Decision

### Gates

| Kind | Fields | Fails when |
|---|---|---|
| `synthetic` | `check` (a synthetic check's name), `max_failure_percent` (0–100, default `api.ReleaseGateDefaultMaxFailurePercent` = 10), `min_samples` (default `api.ReleaseGateDefaultSyntheticMinSamples` = 6) | The candidate answered at least `min_samples` probes, more than `max_failure_percent` of them failed, and its failure rate is higher than the stable revision's over the same window. |
| `metric_change` | `metric`, `comparison` (`above` or `below`), `max_change_percent` (1–1000), `min_samples` | Both windows have at least `min_samples` values, the earlier average is not zero, and the average moved in the gate's direction by more than `max_change_percent` percent of the earlier average. |

A probe passes when the gateway proves the response came from the probed revision and its status is the check's `expected_status`, or any 2xx when the check names none, the rule the check's scheduled runs use. A disabled check is not probed, so its gate is not judged. Deleting a synthetic check deletes the gates that use it, as deleting an SLO does.

### Probing

The route probe worker (ADR-847, ADR-1449) already claims one probe round per app and minute for canaries, and pins each request to a revision with a short-lived challenge. A canary of an app with synthetic gates now joins those rounds:
- each gate's check is sent `api.ReleaseGateSyntheticProbesPerRound` (2) times to the candidate and to the stable revision, with the check's method, path and timeout (capped by the probe request timeout);
- results are counted per gate, revision and minute in `release_gate_probe_results`, pruned with route probe data after `api.RouteHealthProbeRetention`.

The probes carry no customer credentials and write no request telemetry or usage, like route probes.

### Windows

| Judged by | `synthetic` | `metric_change` release window | `metric_change` earlier window |
|---|---|---|---|
| Canary circuit breaker | candidate and stable probes since the rollout started | since the rollout started | the same length of time before the rollout started |
| Post-release watch | not judged: nothing probes the release once its rollout completes | since the rollout completed | the predecessor's baseline window before the rollout started |

### Evidence

`ReleaseGateResult` gains `baseline`: the stable revision's failure percentage for `synthetic`, and the earlier average for `metric_change`. `observed` is the candidate's failure percentage, or the change in percent, signed. Summaries read, for example:
- "checkout failed 5 of 8 probes (62%, max 10%) vs 0% on the stable revision"
- "orders_per_minute fell 41% (31.2 vs 52.9 before the rollout; fails below -30%)"

### Schema

`app_release_gates` gains `synthetic_check_id` (foreign key to `synthetic_checks`, cascading on delete), `max_failure_percent` and `max_change_percent`. The `kind` CHECK widens to the two new kinds. It is replaced online as [ADR-20261020161310](20261020161310-route-insight-fixes-and-alerts.md) did for alert metrics: the widened copy is added NOT VALID and validated, the old one is dropped, and the copy takes the old name. RENAME CONSTRAINT is a contract statement to migration-lint, so a full release applies the migration. The previous release survives every step: it skips gate kinds it does not know.

## Consequences

- An app with synthetic gates sends at most `ReleaseGatesPerAppMax × 2 × 2` = 40 probes a minute while a canary runs.
- A release without a canary (the immediate policy, or a plan without traffic splits) has no synthetic gate protection. Its synthetic checks keep running on their schedule and alert as before.
