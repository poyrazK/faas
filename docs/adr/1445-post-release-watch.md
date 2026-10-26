# ADR-1445 · Post-release watch

- **Status:** accepted (shipped in PR 4872, 2026-10-18); verified on n2 2026-10-17 (safe-release drill post-release case)
- **Date:** 2026-10-17
- **Amends:** [ADR-625](625-first-wake-5xx-rollback-from-request-telemetry.md) (automatic rollback after a release completes), [ADR-1446](1446-safe-releases-by-default.md) (rollback triggers)
- **Follows:** [ADR-1447](1447-explained-rollout-decisions.md) (explained decisions), [ADR-1448](1448-releases-that-change-the-database.md) (automation never crosses a release)
- **Decision:** For `api.PostReleaseWatchWindow` (30 minutes) after a release reaches 100%, apid keeps comparing it with its predecessor's traffic from before the rollout. A clear 5xx or p95-latency regression rolls back to the predecessor automatically, through the same readiness-gated rollback a person uses, and records why on the release. Releases that opted out of automatic rollback (`--rollback-on-5xx=false`) are not watched.
- **Why:** The canary judges a release at 1–50% of traffic for about four minutes. The first-wake rollback (ADR-625) then watches 5xx for five minutes after first traffic, against an absolute bar. After that, nothing compares the release with what it replaced. Regressions that need full traffic or more time to show, such as a slow query under load or a path the canary's share rarely hit, stay live until someone notices, and latency is never judged after the canary.
- **Rejected alternatives:**
  - **Keeping the predecessor running as a hot standby.** A rollback already restores the predecessor from its retained artifact in seconds (6–8 s on n2, ADR-1446), so a warm copy would double the release's memory for little gain.
  - **Running the watch in meterd's canary loop.** The rollout there is finished, and rollback of a completed release is apid's plain path, as for ADR-625. The watch needs only request telemetry, which apid already reads for ADR-625.
  - **Judging crash loops here.** `deployments.liveness_restart_count` is a lifetime counter, so it cannot be compared over windows. The canary's crash-loop gate covers the rollout, and a crash loop at full traffic surfaces as 5xx.

## Decision

### Which releases are watched

apid's watch worker sweeps every `api.PostReleaseWatchCheckIntervalSeconds` (15 s). A release is watched when all of these hold:
- It is live at 100% with `rollout_state = 'complete'`.
- It kept automatic rollback on (`rollback_on_5xx`, the safe default) and has not been rolled back automatically.
- It completed less than `PostReleaseWatchWindow` ago, plus `RollbackOn5xxTelemetryGrace` for late telemetry.
- It is the newest deployment in its scope. A rollback target is older than the release it replaced, so a rollback is never watched against an older version.
- It has a predecessor: the newest superseded deployment in its scope created before it, the same rule ADR-625 uses.

### The comparison

- **New:** the release's requests since it completed.
- **Old:** the predecessor's requests in the `api.PostReleaseWatchBaseline` (30 minutes) before the release's rollout started, when the predecessor served all traffic. An immediate release may not record a rollout start, so its baseline ends when it completed.

Both come from `RequestTelemetryCircuitBreakerSummary`, the summary the circuit breaker and ADR-625 already read. The release regresses when either holds:

- **5xx:** both samples meet the circuit breaker's request floor (20) and the canary's 5xx rule holds (`canary.ServerErrorRegression`). With fewer than 20 old requests, the ADR-625 bar applies instead: at least 5 server errors that are also at least half of the new requests.
- **p95 latency:** both samples meet the floor and the canary's latency rule holds (`canary.LatencyRegression`): at least twice the old p95, and at least 100 ms more.

### The rollback

A regression hands the predecessor to `rollbackAppCore`, as ADR-625 does, with the new trigger `post_release`:
- The rollback keeps artifact verification, the ADR-1448 release guard (automation never acknowledges crossing a release), audit and the verified outcome (ADR-1446).
- The release is marked `last_auto_rollback_reason = 'post_release_regression'`, so it is never evaluated again.
- Its `rollout_decision` records the ADR-1447 decision with the rule `abort_post_release_5xx` or `abort_post_release_p95_latency`, both samples, and the window.
- apid emits `app.post_release_rollback` to the audit log. The rollback's verified outcome is counted in `apid_rollback_outcomes_total{trigger="post_release"}` and `apid_rollback_recovery_seconds`, beside the other triggers.

A release whose rollback would cross a release command keeps serving, as with ADR-625; the worker logs the skip until the window closes.

## Consequences

- A release that regresses at full traffic within half an hour of completing is rolled back without a person, with the evidence on the release.
- Overlap with ADR-625 in the first five minutes is harmless: whichever worker rolls back first marks the release, and the other skips it.
- A quiet app sends too little traffic to compare latency or the 5xx ratio. Only the absolute 5xx bar applies, as before.
- Two values are added to closed sets: `deployment_rollbacks.trigger` gains `post_release`, and `deployments.last_auto_rollback_reason` gains `post_release_regression`. Both constraints are extended in place rather than replaced.

## Rollout

On by default for every release that keeps automatic rollback. Tests cover:
- the candidate query on both stores;
- the decision rules;
- the worker's rollback and decision record;
- the release guard skip.

The safe-release drill on n2 adds a case: a release that answers slowly at full traffic is rolled back by the watch.
