# ADR-20261019071734 · Release gates on customer signals

- **Status:** accepted (shipped in PR 5034, 2026-10-20); n2 metal drill passed 2026-10-26 (canary abort and post-release rollback); amendment 1 (2026-10-27)
- **Date:** 2026-10-19
- **Amends:** [ADR-1446](1446-safe-releases-by-default.md) and [ADR-1447](1447-explained-rollout-decisions.md) (the canary circuit breaker), [ADR-1445](1445-post-release-watch.md) (the post-release watch)
- **Uses:** SLOs (`app_slos`), captured errors (`app_errors`), custom metrics ([ADR-202](202-custom-application-metrics.md), [ADR-745](745-custom-metric-history.md))
- **Decision:** An app can declare release gates on signals it already sends Gregale:
  - an SLO burning too fast;
  - new error groups that the new release introduced;
  - a custom metric crossing a bound.

  The canary circuit breaker and the post-release watch judge every gate alongside their own 5xx and latency rules. A gate that fails aborts the rollout or rolls the release back. The decision records which gate failed and what it measured.
- **Why:** The platform's own rules see only status codes, latency, crashes and memory. A release that returns 200 with wrong prices, fails checkout on a path the canary rarely hits, or throws a new exception class into a catch-all handler passes every one of them. The app already tells Gregale what matters to it, through SLOs, error capture and custom metrics, but releases ignore all three.
- **Rejected alternatives:**
  - **Arbitrary metric queries or webhooks that answer pass or fail.** A remote judge adds a network dependency to every rollout tick and a way for a slow endpoint to stall every release. Gates read only data Gregale already stores.
  - **Gates that hold a rollout instead of aborting it.** A missing sample already holds a low-traffic canary through the circuit breaker's own rules. A gate is a failure detector: when it cannot judge, it does not block.
  - **Comparing custom metrics per revision.** Custom metrics are reported per app, not per deployment, so a canary at 5% cannot be told apart from the rest. A custom metric gate is an absolute bound over the window. It is strongest in the post-release watch, where the release takes all the traffic.

## Decision

### Gates

An app holds at most `api.ReleaseGatesPerAppMax` gates (`app_release_gates`), each with a name unique within the app:

| Kind | Measures | Fails when |
|---|---|---|
| `slo` | The bad fraction of the release's requests in the window. Availability SLOs count 5xx as bad; latency SLOs count requests slower than the SLO's threshold. The burn rate is the bad fraction divided by the SLO's error budget (`1 - objective`). | The release has at least `min_samples` requests, its burn rate is at least `max_burn_rate` (default 10), and its bad fraction is higher than the baseline's. A missing baseline counts as zero. |
| `new_errors` | Error groups whose first occurrence came from the release (`app_errors.deployment_id`), with at least `min_events` events each. | More than `max_new_groups` (default 0) such groups exist. |
| `custom_metric` | The average of the named custom metric over the window. | At least `min_samples` values arrived and the average is `below` or `above` the gate's threshold. |

A gate with too few samples is not judged. The baseline is the predecessor:
- In the canary, it is the stable revision over the same window.
- In the post-release watch, it is the predecessor's traffic before the rollout started.

### Where gates run

- **Canary circuit breaker (meterd).**
  - **When:** Gates run after the platform rules on every tick, over the window since the rollout started (not the current step), so evidence keeps adding up across stages.
  - **If one fails:** The rollout is aborted through the same predecessor restore as a 5xx regression. The decision's rule is `abort_release_gate`, and the event is counted in `canary_progression_circuit_breaker_total{event="abort_release_gate"}`.
  - **If the gates can't be read:** The tick continues on the platform rules alone, and the failure is logged.
- **Post-release watch (apid).**
  - **When:** Gates run after its 5xx and latency rules, over the window since the rollout completed.
  - **If one fails:** The release is rolled back with trigger `post_release` and rule `abort_post_release_gate`.

### Evidence

`RolloutDecision` gains `gates`: one entry for each gate that was judged, with:
- `name`, `kind` and `passed`;
- the measured value and the release's sample count;
- the bound;
- for `slo`, the baseline's bad fraction;
- for `new_errors`, the error classes of the new groups.

Entries hold no routes, messages or metric labels. The decision's summary names the first failing gate, such as "release gate checkout_slo: burn rate 14.2 (max 10) over 812 requests".

### Surfaces

- API:
  - `GET /v1/apps/{slug}/release-gates` lists an app's gates.
  - `POST /v1/apps/{slug}/release-gates` creates one.
  - `DELETE /v1/apps/{slug}/release-gates/{name}` deletes one.
- CLI: `gregale release-gates list|add|rm`.
- Audit events: `release_gate.created` and `release_gate.deleted`.

## Consequences

- Deleting an SLO deletes the gates that use it.
- The first-wake 5xx rollback (ADR-625) does not judge gates; it has its own absolute bar.
- Gates do not run on releases that opted out of automatic rollback (`--rollback-on-5xx=false`) once they are complete, as for the post-release watch. In the canary they always run, because aborting a canary is never a rollback past the predecessor.

## Amendment 1 (2026-10-27): custom_metric gates read the metric's history

`app_custom_metrics` holds one row per app and metric: each push replaces the last value (ADR-202). The `custom_metric` gate counted that table's rows inside its window, so it saw at most one sample. It never reached `min_samples` (default 20) and was never judged. The gate's unit tests used a fake evidence store and missed this; a release-guards GA drill found it.

A `custom_metric` gate now reads its window from the history ADR-745 already keeps: the `gregale_app_custom_metric` series apid exports for Prometheus. The window's sample count is `count_over_time` and its value is `avg_over_time`, both pinned to the window's end (`@ <until>`). `max()` folds the identical series each apid replica exports. meterd's canary progression and apid's post-release watch both pass their Prometheus client. When no Prometheus is configured, the gate falls back to the stored latest value: one sample, so only a gate with `min_samples: 1` is judged.

A gate whose history cannot be read is unavailable and not judged, as for the other kinds. The history only exists while apid exports fresh pushes (`FAAS_CUSTOM_METRIC_HISTORY_ENABLED`, capability `custom-metric-history`). An app that pushes less often than `min_samples` times per window is never judged.
