# ADR-137 · Execution-mode taxonomy (request / service / worker / job)

- **Status:** proposed
- **Date:** 2026-08-30
- **Deciders:** Gregale platform team
- **Forks:** [Epic #1186 — make Gregale a first-class OCI container platform](#1186), sub-task D.1 (execution-mode taxonomy) + D.3 (restart policy). M-2 of the four-Mega-PR plan.

## Context

M-1 (PR #1190, merged 2026-08-30) widened `AppManifest` with the OCI
fields `Healthcheck`, `StopSignal`, `StopGracePeriod` — but the platform
has **one execution shape today**: every customer wake flows through
`Engine.Wake → admitAndDispatch → CreateFromSnapshot/CreateColdBoot →
waitReady → RUNNING → snapshotAndPark → PARKED`. The image-level fields
are dormant until runtime honours them, and there is no first-class
vocabulary for "long-running daemon" vs "one-shot batch job" vs
"replicated service".

The closest existing substrate is **orthogonal** and conflates concerns:

- `WorkloadClass ∈ {http, graphql, grpc, job}` (ADR-050, scan-derived at
  `pkg/state/types.go:4398-4415`) — derived from observed traffic shape.
  It is *routing*, not lifecycle; a worker app that happens to respond
  HTTP on a healthcheck port looks like `http`.
- `InstanceMode ∈ {normal, mirror}` (ADR-125, `migrations/00349`) —
  controls meter skipping for traffic-mirror instances. Two values
  today, neither customer-controlled.

Issue #1186 §D calls for a customer-controlled **execution-mode axis**
with four values: **request**, **service**, **worker**, **job**. M-2
builds the schema, runtime wiring, and admission; the full rolling
deploy / rollback / digest-pinning semantics for `service` land in
M-4 workstream E.

## Decision

### 1. `ExecutionMode ∈ {request, service, worker, job}` becomes a first-class field

`apps.execution_mode text NOT NULL DEFAULT 'request'` (additive; no
column rename, no version bump). Mirrored on `instances.mode` —
existing CHECK `(mode IN ('normal','mirror'))` widens to `(mode IN
('normal','mirror','worker','service','job'))` via migration
`00532_instances_mode_widen.sql` (commit 4).

The mapping `apps.execution_mode → instances.mode` is:

| `apps.execution_mode` | `instances.mode` |
|---|---|
| `request` (default) | `normal` |
| `service` | `service` |
| `worker` | `worker` |
| `job` | `job` |

`normal` and `mirror` continue to exist for legacy mode=`request` (where
`normal` is the default and `mirror` is set by ADR-125 for traffic
mirroring). The meter skip path checks `state.IsMeteredSkippableMode(ins.Mode)`
instead of inline `mode='mirror'`.

`WorkloadClass` (ADR-050) is preserved unchanged for its existing
role — scan-derived routing signal — and is **not folded into
`ExecutionMode`**. The two axes are orthogonal: an OCI image's
`HEALTHCHECK CMD /bin/check` doesn't change `WorkloadClass`, and a
`WorkloadClass=job` may still declare `ExecutionMode=worker` if the
batch is intended to run forever (rare; explicitly allowed).

### 2. `RestartPolicy ∈ {no, on-failure, always, unless-stopped}`

Per `ExecutionMode` defaults:

| Mode | Default `RestartPolicy` | Override allowed? |
|---|---|---|
| `request` | `on-failure` (today's behaviour) | yes |
| `service` | `always` | yes |
| `worker` | `always` | yes |
| `job` | `no` | yes, but `always` rejected at Validate |

Per-mode invariant: `ExecutionMode='job'` with `RestartPolicy='always'`
is a Validate() error. Jobs are run-to-completion; an "always restart a
finished job" pattern is an obvious footgun (queue-loop), and the
explicit rejection catches it at submit time.

### 3. Service replicas scaffold (foundation only)

`deployments.service_replicas jsonb NULL` carries
`{min, max, desired}`. M-2 lays the schema + admission; the engine
maintains a `replicaState{desired, ready, pending}` per service-mode
deployment and **schedules a replacement wake** when `ready < desired`
after a `RUNNING → STOPPED` transition (commit 6).

Rolling deploy / rollback / image-digest pinning for service-mode
deployments are **explicitly out of scope for M-2**; they land in M-4
workstream E. The scaffold M-2 ships absorbs into M-4 without
rewriting — commit 6 lands only the desired-count scaffolding +
admission, not the rolling semantics.

### 4. Restart count, deadline, and grace are per-app

`StartupDeadlineS int`, `MaxRetries int`, `StopGracePeriodS int` —
new fields on `AppManifest`, additive. Per-plan caps replace the gross
`MaxAppManifestStopGracePeriod = 5*time.Minute` cap from M-1:

| Field | Free | Hobby | Pro | Scale |
|---|---|---|---|---|
| `StartupDeadlineS` max | 15 | 60 | 120 | 300 |
| `MaxRetries` max | 0 | 5 | 10 | 20 |
| `StopGracePeriodS` max | 15 | 30 | 60 | 120 |

Per-plan tightening is **commit 10**, on top of commit 3's additive
widening. The constants live in `pkg/api/limits.go` per the "one
table" rule (CLAUDE.md §Conventions).

## Consequences

### Positive

- **Four execution shapes** instead of one, with a customer-controlled
  axis that mirrors the OCI/container vocabulary. Workers and jobs no
  longer pretend to be request-driven apps with a `:8080` healthcheck.
- **Service mode is the foundation** for the rest of #1186 (rolling
  deploys, image-digest pinning across replicas). M-4 workstream E
  absorbs the scaffold without re-plumbing.
- **`WorkloadClass` stays orthogonal** — routing semantics are
  preserved verbatim; ADR-050's PR-A/B/C contracts are unchanged.
- **Meter sampler has a clean axis** (`mode={request,service,worker,job}`)
  for dashboards; workers/jobs bill at the same formula as requests
  (no surprise in GB-h).
- **Per-plan tightening** is in `pkg/api/limits.go` (one table); no
  inline constants.

### Negative

- **`instances.mode` CHECK widening** affects every test that pins the
  constraint set. The pgtest parity suite (`migrations/embed_test.go`)
  catches the breaking case; metal-lima gates commit 4.
- **Per-plan tightening (commit 10)** replaces the M-1 gross 5 min
  `StopGracePeriod` cap. Hobby customers with workloads that need >30 s
  stop grace must set a per-app override (≤30 s) or move to Pro/Scale.
  Documented in PR description + financial-model addendum.
- **Service mode adds RAM pressure** proportional to
  `ServiceReplicasMax × max_concurrency(plan)`. Bounded by the new
  `ServiceReplicasMax` constants (Hobby 1, Pro 5, Scale 20).

### Neutral

- Existing apps (`execution_mode IS NULL`) default to `request`. No
  customer-facing change.
- Mirror-mode skip (ADR-125) is preserved verbatim; the helper moves
  from inline to `state.IsMeteredSkippableMode`.

## Rejected alternatives

- **Per-plan mode allowlist** (Free blocks service/worker, Hobby+1
  worker, etc.). Rejected — telemetry from M-1 hasn't accumulated
  yet; defer to M-3 once we have data on which plans need what.
- **Folding `WorkloadClass` into `ExecutionMode`** (mode=http →
  request, mode=job → job, etc.). Rejected — orthogonal concerns
  (routing vs lifecycle). Folding them would lose the scan-derived
  signal that ADR-050 Phase 4 (characterization boot) depends on.
- **Drop `WorkloadClass` entirely.** Rejected — already wired into the
  scheduler's routing layer; cost of removing is bigger than the
  benefit of the cleaner schema.
- **Service replicas as a separate table.** Rejected — the spec §D
  shape is per-deployment, and `deployments` already carries
  per-deployment jsonb. A separate table forces joins on every wake;
  the jsonb path is additive and migrates cleanly to a column if
  query patterns require it (M-4 follow-up).
- **`RestartPolicy='unless-stopped'` semantics divergence from
  Docker.** Rejected — Docker's `unless-stopped` restarts except after
  an explicit stop. Our stop is always explicit (guest-init exits →
  engine stops), so `unless-stopped` is identical to `always` in our
  model. Documented in `pkg/api/appmanifest.go::Validate()`.

## Cross-references

- **Forced by Mega-PR #2 (M-2) of issue #1186**:
  - `migrations/00528_reserve_slot.sql` through `00531_reserve_slot.sql`
    (commit 2 — slot fences; sidesteps PR #1185 / #1195 / #1197)
  - `migrations/00532_instances_mode_widen.sql` (commit 4 — real DDL)
  - `pkg/api/appmanifest.go` (commit 3 — ExecutionMode, RestartPolicy,
    StartupDeadlineS, MaxRetries, ServiceReplicas fields)
  - `pkg/api/limits.go` (commit 10 — per-plan tier tightening)
  - `pkg/state/types.go` (commit 4 — InstanceMode enum widens)
  - `pkg/sched/engine.go` (commit 6 — Engine.StopInstance + mode
    dispatch + replica scaffold)
  - `pkg/meter/sampler.go` (commit 9 — IsMeteredSkippableMode helper)
  - `pkg/wire/metrics.go` (commit 9 — `metered_mb_seconds_total{mode,plan}`)

- **Loading constraints (existing ADRs this PR must not violate)**:
  - ADR-069 (sidecar containers — hard cap 2): per-mode matrix for
    sidecars evolves separately; M-2 lays the execution-mode axis
    that ADR-069's follow-ups build on. SidecarCapMax=2 is preserved
    verbatim.
  - ADR-125 (mirror-mode skip): widened path checks via
    `IsMeteredSkippableMode`; the `mirror`-only behavior is unchanged.
  - ADR-050 (`WorkloadClass`): preserved verbatim; new field does not
    subsume.
  - ADR-005 (cold boot must always work): unaffected. ExecutionMode
    does not gate wake-path; snapshot reuse remains the same.
  - ADR-009 (identical inner network world): unaffected. 10.0.0.2/30
    per VM, regardless of mode.
  - ADR-019 (jailer uid 20000-29999): unaffected. Each instance gets
    its own uid regardless of mode.
  - ADR-057 (runtime healthcheck probe — HTTP-GET on `:8080`): orthogonal.
    ADR-139 wires OCI `HEALTHCHECK` via vsock reverse-channel;
    ADR-057's host probe continues to gate waitReady for the legacy
    `HealthcheckPath` deployment field.

- **Issue / PR relationships**:
  - **#1186** (parent epic) — M-2 of the five-Mega-PR plan
    (M-1..M-5). This ADR closes sub-task D.1 (taxonomy) + D.3
    (restart policy).
  - **#1184** (broader stateless compute) — ADR-050 parent.
    M-2's execution-mode is the customer-controlled axis; ADR-050's
    WorkloadClass is the scan-derived axis; both coexist.
  - **#474** (guest-init supervisor split) — pre-requisite for M-2
    commit 7 (PID 1 signal handling). M-2 absorbs this work.
  - **#1182** (zero-config first deploy) — out of M-2 scope; consumes
    `ExecutionMode` for `gregale deploy` defaults.
  - **#600** (digest pinning) — informational; service-mode scaffold
    lays the wire field that digest pinning uses (M-4).

- **Spec sections**:
  - §D (container-native lifecycle semantics) — the section this
    ADR implements.
  - §4.7 (meterd — billing math) — formula unchanged; new `mode`
    label surfaces on dashboards.
  - §6.2 (invariants) — preserved verbatim. Worker idle-exempt
    widens an existing carve-out, not a new invariant.
  - §11 (security hardening) — execution-mode is orthogonal to the
    jailer/cgroup/egress surface.
  - §14 (delivery plan) — M-2 ships as part of M8.

- **Tests pinning this ADR**:
  - `pkg/api/appmanifest_test.go` (commit 3) — ExecutionMode default,
    invalid value rejection, per-mode RestartPolicy validation,
    StartupDeadline cap per plan
  - `pkg/api/limits_test.go` (commit 10) — per-plan tier table
  - `pkg/state/types_test.go` (commit 4) — InstanceMode widening
  - `pkg/state/types_pgtest_test.go` (commit 4) — CHECK constraint
    round-trip for every new mode value
  - `pkg/sched/engine_service_replicas_pgtest_test.go` (commit 6) —
    desired=2, kill 1 instance → engine schedules replacement
  - `pkg/sched/reaper_worker_exempt_test.go` (commit 6) — worker
    instance idle 10 min not reaped
  - `pkg/meter/sampler_mode_test.go` (commit 9) — worker NOT skipped,
    mirror IS skipped