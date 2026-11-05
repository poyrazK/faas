# ADR-1321 · Evidence-paced default rollouts

- **Status:** accepted (shipped in PR 4872, 2026-10-18); n2 drill passed 2026-10-15 (see Rollout)
- **Date:** 2026-10-14
- **Amends:** [ADR-1446](1446-safe-releases-by-default.md) (default ladder and probe cadence), [ADR-122](122-safe-releases.md) (preset catalog)
- **Follows:** [ADR-1447](1447-explained-rollout-decisions.md) decision records, [ADR-847](847-synthetic-route-probes.md) route probes
- **Decision:** The default production rollout uses a new `adaptive` preset: the balanced ladder (1% → 10% → 50% → 100%), but a stage advances as soon as the circuit breaker has conclusive, clean evidence against the serving predecessor, after at least one minute, instead of always waiting for its 2-minute timer. `gregale deploy --safe` uses it too. During a rollout the default probe sends 20 probes per revision per round instead of 10, and apid starts each minute's round within about 15 seconds.
- **Why:** On the n2 drills a quiet app took about 9 minutes to reach 100% on probe evidence, and about 21 minutes without probes. That is slow for a one-line fix when a rollback takes 6.8–13.5 s. The breaker already aborts on a small sample with clear errors; it should equally advance once enough responses have come back clean.
- **Rejected alternatives:** Making `balanced` itself evidence-paced (a customer who chose a preset documented as two minutes per stage would see it change under them). A hidden per-deployment flag (two deployments both showing `balanced` would behave differently). Shorter stage timers (advances on no evidence would come sooner too). Sub-minute probe rounds (rounds are claimed once per app and minute across apid replicas, and ADR-847's route health windows are per minute; changing that key would buy about 30 s). Counting probes from the whole rollout rather than the stage (a stage could advance with no new evidence at its traffic share). An operator switch for early advance alone (`FAAS_SAFE_RELEASE_DEFAULT_ENABLED` already turns the whole default off).

## Context

A canary stage advances only when its timer ends (`pkg/canary.Progression`). At that boundary the circuit breaker must see at least 20 requests on each side in the stage, from organic traffic or organic traffic plus synthetic probes (ADR-1446). If it doesn't, the stage waits, and after `CanaryLowTrafficMaxHold` (5 minutes) it advances on the absence of bad news.

The ADR-1446 default probe is one round per app and minute: 10 `GET /` probes per revision, recorded under the minute. meterd counts rounds whose minute starts at or after the stage started, and apid's worker polls once a minute at an arbitrary offset. A quiet stage therefore needs two rounds that both start after it, which takes one to three minutes. With 2-minute timers that is often three minutes per stage, so about 9 minutes for the default ladder.

## Decision

### The `adaptive` preset

A catalog stage gains `MinDuration`: the earliest the stage may advance on conclusive evidence (0 means timer only). `adaptive` has the balanced percentages, with `MinDuration` 1 minute and `Duration` 2 minutes for the 1%, 10% and 50% stages. Every other preset keeps `MinDuration` 0 and is unchanged.

### Early advance

Between `MinDuration` and `Duration`, a stage advances when either:

- the breaker returns advance on the full organic comparison against a serving predecessor: every signal readable, at least 20 requests and 20 CPU samples on each side in the stage, no regression (rule `advance_healthy`); or
- organic plus probe responses meet the 20-response floor on both sides, with no 5xx regression and readable, clean OOM and liveness signals (rule `advance_probe_evidence`).

The safe-release health-alert gate and APID's route-health and profile gates still apply to an early advance. A clear regression aborts at any time, as before. With no predecessor, inconclusive evidence, or any other reason to hold, the stage waits for its timer, and ADR-1446's boundary rules apply unchanged: probe evidence at the boundary, then the 5-minute bound for no news. The no-news advance never happens early.

One minute is the floor because:

- OOM kills and liveness restarts are read over the whole rollout, so a crash loop or OOM seen in any stage still stops it.
- Each traffic share is observed for at least two progression ticks (30 s each) and several Prometheus scrapes.
- The candidate is watched for at least three minutes before it takes all traffic, and the 5-minute first-wake 5xx rollback follows completion.

APID enforces the same rule. Its worker advance transaction already rejected a step whose stage had not run for its duration. It now requires `MinDuration` instead only when the stage has one and the worker's validated record is `advance_healthy` or `advance_probe_evidence` (`release.StageDwell`). A worker without a record, with the no-news rule, or on a timer-only preset still waits for the timer.

The decision record (ADR-1447) is unchanged: an early advance carries its usual rule, and its window is shorter than the stage's timer.

### Default and `--safe`

`release.ApplyDefault` fills `adaptive` (`api.DefaultReleaseCanaryPreset`). `gregale deploy --safe` asks for `adaptive` and still waits for 100%. Explicit presets keep their timers, and customers can choose `adaptive` explicitly. `deployments.canary_preset`, the API enums and the CLI accept the new name.

### Probing more during a rollout

The default probe sends `api.DefaultRouteProbeRequestsPerRound` (20, equal to the breaker's request floor) per revision per round, so one round inside a stage meets the floor. ADR-847 route probes keep `RouteHealthProbeRequestsPerMinute` (10). apid's worker polls every `RouteHealthProbePollInterval` (15 s). Rounds are still claimed once per app and minute, so a minute's round now starts within about 15 s of the minute. A round keeps its 55 s budget (`RouteHealthProbeRoundTimeout`), and old probe data is pruned at most once a minute.

### Expected timing

For a quiet app, a stage's first counted round arrives at most about 75 s after the stage starts, and the next progression tick is at most 30 s later. Each stage takes about 1 to 1.75 minutes, so about 3.5 to 5 minutes to 100%, down from about 9. For an app with traffic, each stage takes about 1 to 1.5 minutes once 20 requests reach the candidate. A stage at 1% of a moderately busy app may still need its timer, or the probes.

## Consequences

- Default releases reach 100% about twice as fast. Exposure at 1% and 10% is shorter, so a regression that needs more than a minute and fewer than 20 responses to show reaches more traffic sooner. The backstops are rollout-wide OOM and liveness signals, the first-wake 5xx rollback, and rollbacks that finish in seconds.
- A safe-release alert rule with a window longer than a minute may fire after a stage has already advanced. Its rollback action still applies.
- Probes per revision double per minute during a rollout (20 instead of 10 `GET /`), but the rollout lasts about half as long, so a quiet app's total probes and its billed wake time for the rollout drop.
- `canary_preset` gains `adaptive` in the database check, both OpenAPI specs, the SDKs, the CLI and the per-preset advance counter. Deployments that name `balanced` behave exactly as before.
- No new metrics: early advances are counted in `canary_progression_advanced_total{canary_preset="adaptive"}` and visible in decision records.

## Rollout

Ship with the preset, the early-advance rule and the probe cadence together. The safe-release drill's flag-free case asserts that a default release of a quiet app is stamped `adaptive` and completes before the balanced timers would allow (6 minutes), with at least one advance whose decision window is shorter than its stage's 2 minutes.

The drill passed on the n2 node on 2026-10-15 (all six cases, leakcheck clean). A flag-free release of a quiet app reached 100% in 248 s, down from about 9 minutes under ADR-1446: three `advance_probe_evidence` advances after 66 s, 90 s and 90 s, each on one or two probe rounds of 20 per side. The custom 1%@30s canary advanced on probes after 54 s (170 s in the ADR-1447 drill), the late-failing release aborted on probes after 76 s (127 s before), and the bad release, crash loop and worker-down fallback behaved as before; rollback recovered in 10.5 s. The first drill run failed the flag-free case at 7m12s with no early advance: APID's worker transaction still enforced the full stage timer, which the pkg/canary unit tests could not see because they stub APID. `release.StageDwell` fixed it, and `TestSafeDeployWorkerAdvancesPacedStageOnEvidence` now covers the worker route.
