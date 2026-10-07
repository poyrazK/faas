# ADR-644: Workflow run diagnostics and resume preview

- **Status:** accepted
- **Date:** 2026-10-07
- **Extends:** ADR-573 safe continuation, ADR-640 deployment pins and ADR-642 queue health
- **Decision:** Add read-only `GET /v1/workflows/runs/{id}/diagnostics` and the
  corresponding tenant-self route. `gregale workflows diagnose <run_id>` prints
  the same observation and recovery preview; Go, Node and Python SDKs expose
  both routes. No migration or new continuation mechanism is introduced.
- **Observation:** Return status, original code pin (or explicit legacy-unpinned
  status), observation time, durable scheduling/capacity reason, future wake,
  due age and expired-lease status. Step metadata distinguishes actions,
  iterations, joins, timers, events, callbacks and conditions. Omit inputs,
  outputs, errors, tenant identity, callback tokens and integration credentials.
  `ready` describes dispatch admission, not an execution or availability promise.
- **Recovery:** Share the existing continuation planner with the write path.
  Make planner iteration deterministic and retain sentinel error identity.
  Report the first precise planner blocker plus independent current admission
  blockers as stable codes and fixed explanations. Sort reopened and preserved
  names. Preserve guard decisions, completed batch items, successful actions,
  action idempotency keys and code pins. Temporary blockers can coexist with
  a structurally valid continuation plan.
- **Admission:** Check account/plan/app eligibility, maintenance, tenant
  requirements and active app link, default deployment, original pinned code,
  integrations, running attempts and active-run quota. Tenant revocation and
  missing pinned code also block actual continuation admission; dispatch and
  gateway checks remain authoritative at execution. The API reports its runtime
  flag; one replica's flag does not attest fleet readiness. Authorization is
  enforced by the mutation endpoint; preview grants no write permission.
- **Consistency:** Memory observes under its mutex. PostgreSQL uses a
  repeatable-read, read-only transaction with a five-second deadline and SQLC
  queries without row/advisory locks. Ownership and optional tenant scope are
  checked inside the snapshot; native Customer Operations custody is excluded.
  Read errors return unavailable rather than a successful eligibility claim.
  Both routes return `Cache-Control: no-store`.
- **Limits:** Existing definition/iteration limits bound persisted metadata.
  Queue checks share dispatcher capacity calculations and legacy lease fallback.
  Future waits/retries do not accumulate due age; capacity-blocked work and
  expired leases do. No provider probe or deduplication-TTL guarantee is made.
- **Concurrency:** Preview reserves no slot, claims no lease, resets no steps,
  changes no audit/history and does not increment `resume_count`. Submit the
  observed count to existing resume, which locks and rechecks state and quota.
  Changes can invalidate a preview. Batch/automatic recovery and arbitrary
  step/input/code overrides remain separate future work.
- **Rollout:** Upgrade API servers and SDK/CLI after the existing workflow schema
  and deployment-pin rollout. No schema change is needed. Older servers return
  404; clients must not interpret that as permission to recover. Removing this
  feature does not discard customer state.
