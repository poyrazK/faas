# ADR-625 · First-wake 5xx auto-rollback is evaluated by apid from request telemetry

- **Status:** accepted
- **Date:** 2026-10-06
- **Amends:** [ADR-200](200-auto-rollback-on-every-plan.md)
- **Related:** ADR-122 (canary ladder), ADR-127 (request telemetry), ADR-602 (alert rollback)

## Context

`gregale deploy --rollback-on-5xx`, and `--safe` which implies it, store
`deployments.rollback_on_5xx = true` (migration 00354). The intended design
had schedd subscribe to `wake.response_5xx` events, bump `first_5xx_count` via
`BumpFirst5xxCount`, and call an apid-internal auto-rollback endpoint.

None of that was ever built. No event, no subscriber, no endpoint and no caller
of `StampFirstWake` or `BumpFirst5xxCount` exists in any commit. ADR-200 says
the counters "are stamped on every deployment regardless of plan"; that was
never true. PgStore's `StampFirstWake` could not even run: it bound an integer
to a `$2::text` parameter, which pgx rejects.

production-us hunt #4 (2026-10-06, rc.243) found the gap. A release deployed
with `--rollback-on-5xx` answered 100% HTTP 500 for over a minute and stayed
live. `request_telemetry` recorded 30 of its 500s. The docs (`deploys.md`)
promise the revert.

## Decision

apid evaluates the opt-in, using the request telemetry already ingested for
every proxied request.

1. **Owner.** A background worker in apid (`runRollbackOn5xxWorker`). apid is
   the only writer of customer-intent tables, so it owns the deployment
   transition. schedd still owns instances; it reacts to the rollback's
   existing `snapshot_prime` readiness handoff, as it does for a manual
   rollback.
2. **Candidates.** The worker considers a release only when all of these hold:
   - it is live, at 100% traffic, with `rollout_state = 'complete'`;
   - it has `rollback_on_5xx`;
   - it has no `last_auto_rollback_reason`;
   - its window is unopened, or closed less than `RollbackOn5xxTelemetryGrace` ago.

   A canary in flight stays with the meterd ladder's health gates and circuit
   breaker. The query is `ListRollbackOn5xxCandidates`, via sqlc.
3. **Signal.** `RequestTelemetryCircuitBreakerSummary` gives the release's
   request and 5xx totals. It is the bounded per-deployment summary that the
   meterd circuit breaker already reads. Responses count from the release's
   creation, so errors seen before the window opened still count.
4. **Window.** When the worker first observes traffic for a release, it calls
   `StampFirstWake` with `RollbackOn5xxWindowMinutes` (5). Telemetry
   timestamps are minute buckets, so the bucket that holds the window's end
   still counts.
5. **Threshold.** At least `RollbackOn5xxMinServerErrors` (5) 5xx responses,
   which are also at least `RollbackOn5xxMinErrorPct` (50%) of the release's
   requests. The ratio stops one failing route on a busy release from
   reverting it. The constants live in `pkg/api/limits.go` and do not vary by
   plan, per ADR-200.
6. **Action.** The worker hands the release's predecessor to
   `rollbackAppCore`, the readiness-gated rollback that the REST and dashboard
   surfaces share. The predecessor is the newest superseded deployment in the
   same scope that was created before the release.
   - That path keeps its artifact verification, binding-release policy, API
     contract gate, audit and activity records.
   - The release keeps serving until the target is ready.
   - The worker then stamps `last_auto_rollback_reason = 'threshold_exceeded'`.
   - A release with no predecessor keeps serving.

`first_5xx_count` and `BumpFirst5xxCount` stay unused; the threshold reads
telemetry directly, so there is no second counter to drift.

## Consequences

- `--rollback-on-5xx` and `--safe` do what their help and the deploy docs say.
- A revert takes up to one check interval (15 s) plus telemetry ingest lag
  after the threshold is crossed, then the normal rollback readiness time.
- Detection depends on request telemetry. If its ingest stops, the release is
  not reverted and nothing reports why. The alert and canary paths share the
  same dependency.
- No migration: every column already exists.

## Validation

- `TestRollbackOn5xxSweepRollsBackAFailingRelease` (cmd/apid) covers four steps:
  - no traffic leaves the release untouched;
  - healthy traffic opens the window and changes nothing else;
  - a failing window moves the predecessor into readiness-gated rollback and
    records the reason;
  - a rolled-back release is never evaluated again.
- `TestRollbackOn5xxBreached` pins the threshold.
- The `rollback_on_5xx_candidates_track_the_opt_in` conformance case runs on
  both stores and covers `StampFirstWake` and `MarkAutoRollback`. It is what
  caught the PgStore interval encoding bug.
