# ADR: Data API health cache bounds and process liveness

Date: 2026-11-09
Status: Accepted
Related: [ADR-650](650-schema-generated-data-api.md), [ADR-078](../faas_implementation_spec.md) (liveness probes, spec §6 failure table), [ADR 20261026131131](20261026131131-data-api-hosted-tls-roots-and-snapshot-sessions.md)

## Context

1. **The `/healthz` cache answered for engines that were gone.** PR #6775 made
   `/healthz` reuse any completed set of checks for 5 seconds, so public
   polling could not keep the database busy. The cached set was reused
   whether it was ready or not, and nothing forgot it when the engine
   stopped, started, reloaded its schema or was restored:
   - a "not yet" answer held a starting engine back from readiness for up to
     5 s on every cold boot;
   - a restored instance answered from the set cached just before the
     checkpoint (the monotonic clock resumes where the snapshot paused it),
     reporting ready while PostgREST was still restarting;
   - `gregale data-api dev` kept reporting ready after a failed reload.
   Four real-engine acceptance tests failed on it (admission, two pool
   observation tests and the starter), reproduced against PostgreSQL 16 and
   PostgREST 14.3, and passing with the cache off.
2. **Liveness used SQL readiness.** vmmd probes every Hobby, Pro and Scale
   instance at `/healthz` (5 s period, 3 consecutive failures). A 503 counts
   as a failure; the load grace covers only timeouts and refused connections.
   The Data API's `/healthz` answers 503 whenever SQL readiness fails, which
   a saturated two-connection pool or a short database outage causes while
   the runtime is healthy. Three such answers destroyed the instance,
   dropping in-flight requests and marking its snapshot stale; three
   destroys in 300 s parked the app `evicted_cold`.
3. **The acceptance suite hid its own failures.** `scripts/test-data-api.sh`
   stopped at the first failing real-engine group, so ~35 later test files
   did not run from 2026-11-07 to 2026-11-09, and the workflow's PR path
   filter did not include the runtime template, so runtime changes merged
   without the suite.

## Decision

1. `/healthz` shares a running set of checks with concurrent callers and
   reuses only a completed **ready** set, for `DataAPIHealthCache` (5 s). An
   unready set is never reused. The runtime forgets every set in
   `before_checkpoint`, `after_restore` and on a schema reload
   (`invalidateHealth`). `gregale data-api dev` and real-engine tests that
   observe transitions set `healthCacheMs` to 0.
2. Data API deploys keep startup readiness on `/healthz` and send a
   per-deployment liveness probe on `/livez` (`api.DataAPILivenessPath`),
   which checks only that the gateway process answers. The multipart deploy
   gains a `liveness_probe` field. apid applies it where the plan has
   liveness probes and ignores it on Free, which has no probe to replace (the
   CLI cannot see which plan bills an organization's app). The dashboard's
   saved Data API setup sends the same probe. An engine that exits still
   ends the runtime process, which liveness and restart policy observe.
3. Every real-engine group runs; the script fails at the end naming each
   failed group. The workflow runs on changes to the runtime templates and
   the Data API CLI commands.

## Consequences

- Public `/healthz` can report ready for up to 5 s after SQL readiness is
  lost, never after the engine restarts; `data-api health` and
  `data-api wait` remain the readiness tools.
- A wedged gateway is still caught by liveness. A wedged engine whose
  process stays up is not; requests then fail with `data_api_unavailable`
  and `/healthz` reports it. This trades that rare case for not destroying
  healthy runtimes under load or during a database blip.
- Older apid releases ignore the unknown `liveness_probe` field, so a new CLI
  against an old control plane keeps the previous behaviour.
- Existing Data API deployments keep the `/healthz` liveness probe until
  their next deploy.
