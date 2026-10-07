# ADR-646: Compose dependency readiness gates for project releases

- **Status:** implemented
- **Date:** 2026-10-07
- **Problem:** Project admission retained Compose dependency names while
  discarding conditions. Topological enqueue order allowed a dependent release
  to advance while its dependency's new deployment was still being prepared.
- **Decision:** Capture explicit `depends_on` conditions in scan plans and
  source-managed app metadata. `service_started` retains admission ordering.
  `service_healthy` gates initial release activation on an exact dependency
  deployment in the same account, project, environment, and PR preview cohort.
  The dependency must be live with positive traffic and must not be parked.
  This uses the dependency release's existing readiness and verification gates;
  parked request apps need not be woken merely to observe a ready release.
- **Admission:** CreateDeployment captures the latest accepted revision of
  each required project member in the same transaction as candidate admission.
  Names, app IDs, and deployment IDs live in a separate immutable gate journal,
  independent of image runtime profiles and mutable app metadata. A missing
  same-environment deployment rejects admission. Project apply and GitHub pushes
  admit selected dependencies first and skip a dependent when a selected
  dependency's admission failed, preventing fallback to its older serving row.
  Untouched dependencies use their latest accepted deployment. Concurrent
  project applies remain separate per-app admissions, not an atomic release
  cohort: each candidate retains exactly what its admission observed.
- **Release boundary:** Imaged publishes the candidate's own validated snapshot
  or accepts its existing worker/job readiness proof, then checks dependencies
  before smoke and promotion. MarkDeploymentLive and its latest-revision variant
  recheck under the same transaction/lock that moves traffic. PostgreSQL shared
  locks on dependency deployments prevent a check-versus-retirement race; an
  update trigger backstops direct initial status-to-live writes. MemStore checks
  under its mutex, including generic status updates. No worker writes scheduler
  instance state or calls vmmd to enforce this gate.
  Failed or superseded candidates that never served remain gated; historical
  restoration exemptions use the existing serving history, not status alone.
- **Waiting and recovery:** The first readiness observation starts one durable
  15-minute deadline. Restart/redelivery does not reset it. Pending readiness
  defers the existing notification by five seconds without consuming its bounded
  delivery failure budget or holding a worker in a polling loop. Storage errors
  retain ordinary notification retries. Failed, superseded, cancelled, missing,
  or parked dependencies fail the candidate; timeout uses
  `dependency_readiness_timeout`. The previous release keeps serving throughout.
  Stage retries copy the exact dependency pins with a fresh wait deadline.
- **Status:** `stage_state.dependency_gate` includes the captured dependencies,
  waiting/ready/failed state, blocker, and persisted deadline. CLI stage/status
  summaries and dashboard stage summaries display the blocker; JSON retains
  the full progress. Scan plans and generated SDKs expose
  `depends_on_conditions`.
- **Boundaries:** This gates initial release activation, not process startup,
  continuous dependency availability, or subsequent traffic changes to an
  already-live release. Candidate processes may boot while waiting. A completed
  release's explicit rollback retains existing recovery semantics. Dependency
  conditions do not pin subsequent inter-service calls to a deployment; the
  existing service resolver owns routing. Completion conditions, optional healthy
  dependencies, and healthy managed/external targets are rejected explicitly.
  Jobs cannot certify service health through their artifact-only readiness.
  Held environment clone/promotion deployments cannot acquire ordinary gates;
  graph-wide dependency integration remains separate. Source-built Compose
  healthcheck override support remains the ADR-642 boundary.

Rollout applies the schema migration before the admission and imaged updates.
Enable new declarations after those components are updated. Deployments accepted
before the new admission contract retain their legacy release behavior.

Validation covers condition import and rejection, reconciliation/removal,
selected admission failures, memory/PostgreSQL promotion and generic-write
guards, promotion racing a dependency retirement transaction, immutable pins
across edits and stage retries, scope isolation, fixed
deadlines, real notification deferral and restart replay, terminal dependency
failure, and unchanged predecessor traffic. No VM lifecycle implementation is
changed by this decision.
