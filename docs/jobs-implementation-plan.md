# ADR-099 Jobs — implementation plan

- **Status:** proposed (2026-08-13)
- **ADR:** `docs/adr/099-jobs.md` (merged at `c3d8858d`)
- **Owner:** schedd / apid / pkg/state
- **Branch under plan:** `release/adr-099-jobs` (merged); subsequent work on
  `release/jobs-pr-N` branches off `main` after each prior PR ships.

## Scope check — five ADR-099 errata surfaced by verification

Five pre-existing-codebase assumptions in ADR-099 were wrong when verified
against `main@c3d8858d`. Each erratum is reflected in the PR cluster below.

| # | Erratum                                                                                                                          | Surfaces                                                                                            | Fix in PR    |
|---|----------------------------------------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------------------------------|--------------|
| 1 | `instances.kind` column does not exist (only `state` has a CHECK). ADR-099 §Decision 3 assumed `ALTER ... DROP CONSTRAINT instances_kind_check`. | `migrations/00001_init.sql:81-94`                                                                  | PR-A (00232) |
| 2 | `instances.app_id` is NOT NULL FK to `apps(id)`, and `usage_minutes.app_id` is NOT NULL. ADR-099 §Decision 7 assumed `app_id=NULL` would work for jobs. | `migrations/00001_init.sql:83`, `migrations/00001_init.sql:97-99`                                  | PR-A (00232, 00233, 00234) |
| 3 | `Engine.Wake(ctx, appID, deploymentID, scope)` takes no `kind` param. ADR-099 §Decision 3 said `EnsureInstance(kind=job_task)`. Adding `kind` would touch ~12 call sites. | `pkg/sched/engine.go:967`                                                                          | PR-C         |
| 4 | `ReapIdle` reaps RUNNING instances; jobs need to be exempt. ADR-099 §Decision 3 said "the reaper (§6.1) gains one branch" — `InstanceInfo` needs a `Kind` field and the SQL hydration needs to project it. | `pkg/sched/reaper.go:25-37`, `pkg/sched/reaper.go:205`                                              | PR-C         |
| 5 | Spec §6 prose does not enumerate "wake or build" — pkg/sched/reaper.go:1369 references "workers are reaper-exempt" but no other surface needs spec update beyond §6 + §6.1. Smaller than the ADR implied. | `docs/faas_implementation_spec.md` §4.7.4 (new), §6 (widening to "wake, build, or job_task")        | PR-F         |

The ADR body remains accurate at the architectural level (3 tables, 7 plan
caps, dispatch tick, guest-init branch). The PR cluster corrects the
implementation-detail deviations.

## PR cluster

Seven PRs, six of them code. PR-0 is the rate-limiter primitive the ADR
calls out as load-bearing (also required by ADR-080 Risk #1). PR-F is
docs. Each PR is reviewable in ~10 min per CLAUDE.md.

```
PR-0  pkg/sched/rate_limit.go        ──┐
                                       ├──► PR-C (depends on both)
PR-A  schema (jobs tables +          ──┘
        instances.kind +
        app_id/job_id/usage_minutes)

PR-B  pkg/state CRUD + sqlc ────► PR-C (state queries) ────► PR-D (apid routes) ────► PR-E (CLI/e2e/dashboard) ────► PR-F (docs)
```

### PR-0 — `pkg/sched/rate_limit.go::EnsureInstanceRateLimit` (sibling PR)

Independent of ADR-099; required by it AND ADR-080 Risk #1. Ships first.

- `pkg/sched/rate_limit.go` — new. Token-bucket per app + per account.
  Per-plan burst ceiling: Free 1 / Hobby 5 / Pro 20 / Scale 100 wakes/min.
- `pkg/api/limits.go` — `WakeBurstPerApp = [4]int{1, 5, 20, 100}`,
  `WakeBurstPerAccount = [4]int{1, 10, 30, 150}`.
- `pkg/sched/engine.go::EnsureWake` — consult the limiter before admission.
  Returns `WakeResult{AtCapacity: true, Reason: "rate_limit"}` on throttle.
- `pkg/sched/rate_limit_test.go` — table-driven: burst, refill, per-app
  isolation, per-account isolation.
- Migrations: none.
- Acceptance: existing wake tests still pass; new test
  `TestEnsureWake_RateLimit_ThrottlesAfterBurst`.

**Cross-team note**: ADR-080 owner needs to coordinate (PR #868 is the
issue tracker). Don't merge PR-0 here until they confirm they're not
already shipping an equivalent primitive — if they are, **delete PR-0
and rebase onto their branch**.

### PR-A — schema (jobs tables + instances.kind + nullable app_id)

The largest schema PR. Slot-reserved at **00231–00234**. Split into
three migrations to keep each goose transaction short.

- **00231_jobs.sql** — `public.jobs`, `public.job_runs`, `public.job_tasks`
  with the CHECK constraints from ADR-099 §Decision 1. Indexes
  `job_tasks_ready_idx`, `job_runs_account_idx`.
- **00232_instances_kind_job.sql** —
  - `ALTER TABLE instances ADD COLUMN kind text NOT NULL DEFAULT 'wake'`,
    add `instances_kind_check` CHECK (`kind IN ('wake', 'build', 'job_task')`).
  - `ALTER TABLE instances ALTER COLUMN app_id DROP NOT NULL`,
    `ALTER TABLE instances ADD COLUMN job_id bigint REFERENCES public.jobs(id)`.
  - Add `instances_job_id_idx (job_id) WHERE job_id IS NOT NULL` partial index.
- **00233_usage_minutes_app_nullable.sql** —
  - `ALTER TABLE usage_minutes ALTER COLUMN app_id DROP NOT NULL`,
    `ALTER TABLE usage_minutes ADD COLUMN meter_kind text NOT NULL DEFAULT 'app'`,
    add `usage_minutes_meter_kind_check` CHECK (`meter_kind IN ('app','job')`).
  - `CREATE INDEX usage_minutes_job_idx ON usage_minutes (account_id, minute DESC)
     WHERE meter_kind = 'job'`.
- **00234_apply_walk_test_pins.sql** — `apply_walk_test.go` test
  pins for the new migrations; also fences 00235–00240 as
  `select 1;` reserves per the cross-PR slot fence pattern
  (`migrations/00235_reserve_slot.sql` … `00240_reserve_slot.sql`).
  These 6 fences are NOT consumed by PR-A; they are coordination
  artifacts so PR-B/C/D/E/F can race with other in-flight
  clusters without re-doing the renumber dance from PR #858.
- `pkg/state/pgstore.go` — wire `instances.kind` into `CreateInstance`
  + `UpdateInstance` SQL.
- `pkg/sched/engine.go::EnsureWake` — set `kind='wake'` (default) on insert.
  `pkg/sched/engine.go::WakeJob` (PR-C) will set `kind='job_task'`.
- Migration slot pre-check: PR-A must run `git ls-tree origin/main migrations/`
  + enumerate open PRs (`gh pr list --state open --json files`) before
  pushing — same precedent as PR #867. Open a tracking issue for the
  slot fence at most once per cluster.

### PR-B — `pkg/state` CRUD + sqlc

No migrations; sqlc regen only.

- `pkg/state/jobs.go` — sqlc-generated: `CreateJob`, `GetJob`,
  `ListJobs`, `DeleteJob`, `UpdateJob`, `CreateRun`, `ClaimTasks`
  (the `FOR UPDATE SKIP LOCKED` batch claim), `MarkTaskSucceeded`,
  `MarkTaskFailed`, `MarkTaskTimeout`, `MarkTaskCancelled`,
  `MarkTaskOOM`, `RecomputeRunStatus`.
- `pkg/state/jobs_test.go` — every state transition in ADR-099
  §Decision 2 + the run-level fan-in aggregator.
- `pkg/state/pgstore.go::WithJobStore` setter; `cmd/apid/main.go` wire-up.
- No apid surface yet — `pkg/state` exposes the CRUD but routes
  land in PR-D.
- Migrations: none (sqlc output only).

### PR-C — schedd dispatch + Engine.WakeJob + reaper + guest-init

The architectural PR. Heavy on `pkg/sched/`.

- `pkg/sched/engine.go` — new `Engine.WakeJob(ctx, jobID, runID,
  taskIndex)` (~150 LOC mirroring `Engine.Wake`). Boot path:
  vmmrouter → jailing → snapshot lookup → readyz. Writes instance
  row with `app_id=NULL, job_id=:id, kind='job_task'`.
- `pkg/sched/dispatch_jobs.go` — `runJobsTick` (ADR-099 §Decision 6).
  `LISTEN job_tasks_queued`; 1 s wall-clock tick; consults
  `EnsureInstanceRateLimit` (PR-0) + `admitGate` (RAM ceiling) +
  per-run running-task count (parallelism cap) before admitting.
- `pkg/sched/jobs.go` — `Engine.AllocateJobTask` /
  `Engine.MarkJobTaskTerminal` helpers (called from
  dispatch_jobs.go + the exit-code DGRAM handler).
- `pkg/sched/reaper.go::InstanceInfo` — add `Kind string` field.
  `ReapIdle` and `ReapAggressive` skip rows where `Kind == 'job_task'`.
  SQL hydration in `pkg/sched/dispatch_jobs.go::loadRunningInstances`
  (new) projects `kind`.
- `pkg/sched/loop.go::Loop` — register `runJobsTick` alongside
  `runCronTick`. Add `WithJobDispatcher` setter.
- `cmd/schedd/main.go` — wire `WithJobDispatcher` + the
  per-run running-task counter.
- `guest/init/app.go` — branch on `app.json.kind`. New
  `guest/init/job_supervisor.go` spawns `command[0]` with merged
  env, captures exit code, writes one-shot DGRAM
  (`port=1026, msg_type=3`, distinct from `VsockResumePort=1024`
  and characterize `1025/2`), then `os.Exit(code)`. Watchdog is
  `task_timeout_s` (per-job), not the 5 s `snapshot_and_park`.
- `pkg/fcvm/vsock.go` (or its current equivalent) — register
  the `1026/3` port alongside `1024` and `1025/2`.
- Tests:
  - `pkg/sched/dispatch_jobs_test.go` — claim, admit, backoff,
    parallelism cap, rate-limit interaction.
  - `pkg/sched/engine_wake_job_test.go` — boot path mirrors
    `pkg/sched/engine_test.go`'s Wake tests.
  - `guest/init/job_supervisor_test.go` — exit-code capture,
    env merge, watchdog SIGTERM.
  - `pkg/sched/reaper_test.go` — job_task rows skip `ReapIdle`.

### PR-D — apid routes + OpenAPI + SDK

No migrations; OpenAPI regen.

- `cmd/apid/handlers/jobs.go` — 11 routes from ADR-099 §Decision 10.
  Same handler-shape as `cmd/apid/handlers/crons.go` (the existing
  cron routes from ADR-090 are the template).
- `pkg/api/limits.go` — 7 plan-bound constants from ADR-099
  §Decision 5. Cap enforcement in `apid` at accept time
  (returns 402 `jobs_not_allowed` for Free; 429 `job_quota_exceeded`
  for per-account cap).
- `pkg/api/dto.go` — `Job`, `JobRun`, `JobTask` DTOs.
- `pkg/api/spec/openapi.yaml` — schemas + 11 routes.
- `pkg/apid/openapi.yaml` — regenerated via `make spec-sync`.
- `pkg/api/client.go` — typed methods.
- `sdk/{go,node,python}/...` — regenerated.
- `pkg/api/errors.go::StatusForCode` — `CodeJobsNotAllowed`,
  `CodeJobQuotaExceeded`, `CodeJobTaskNotFound`,
  `CodeJobRunCancelled`, `CodeJobDeadlineExceeded`.
- `pkg/api/spec/apidpath_reservations` — pin the 11 new paths so
  `TestApidPathReservations_Documented` keeps passing.
- Tests:
  - `cmd/apid/handlers/jobs_test.go` — happy-path + every 4xx/402/429.
  - `pkg/api/dto_test.go` — JSON round-trip.
  - SDK smoke test (existing test harness covers this).
- Acceptance: `make spec-sync` clean; `spec-check` CI job green;
  OpenAPI drift test green.

### PR-E — CLI + e2e + dashboard

No migrations; CLI + tests + dashboard template.

- `cmd/gregale/cmd/jobs.go` — 7 subcommands from ADR-099 §Decision 9.
  Mirrors `cmd/gregale/cmd/crons.go` shape. Cookie/bearer-aware
  (cookie-only routes CLI rejects — see memory `cookie-only-routes-cli-rejects`).
- `cmd/gregale/cmd/jobs_test.go` — flag parsing, output formatting.
- `pkg/e2e/jobs_e2e_test.go` — full lifecycle:
  create job → run with `--tasks 100 --parallelism 10` →
  verify task fan-out → retry on simulated OOM → cancel mid-run
  → dead-letter after exhaustion → log tail → RAM cap rejection
  → Free plan 402 → meterd roll-up.
- `pkg/state/jobs_e2e_helpers.go` (or testutil) — fixture helpers.
- `dashboard/jobs.html` + `dashboard/job_run.html` — thin
  server-rendered views (per ADR-011 `ux_spec.md`). Mirrors the
  crons dashboard shape.
- Acceptance: `make test` clean; `make test-metal` (job-task
  VM path) green; `make leakcheck` zero leaked netns/TAPs/jail uids.
  Per CLAUDE.md "If a change touches VM lifecycle, run test-metal
  and leakcheck before calling it done."

### PR-F — docs (spec + STATUS + runbook)

Docs-only PR; lands last.

- `docs/faas_implementation_spec.md` §4.7.4 — new paragraph on
  jobs (entity model, lifecycle, billing, plan caps). Reference
  ADR-099 + this plan.
- `docs/faas_implementation_spec.md` §6 — widen "wake or build"
  to "wake, build, or job_task". Reaper rule for `kind='job_task'`.
- `docs/STATUS.md` — new M7.x entry pinning e2e test to wire
  surface (per the convention from ADR-090).
- `docs/runbooks/JobsBacklog.md` — operator-facing: `jobs_queue_depth`
  > X for Y min, `jobs_dispatch_rejected_total{reason=ram_cap}` spikes,
  dead-letter rate above threshold. Mirrors
  `docs/runbooks/FaasBuildQueueBacklog.md` shape.
- `docs/runbooks/JobTaskOOMStorm.md` — when job tasks get OOM-killed
  en masse (`error_kind='oom'`), what to check (image memory budget,
  cgroup scope, plan RAM ceiling).
- Acceptance: `make spec-check` green; `make lint` green.

## Non-code coordination tasks

These ship alongside the PRs (not in them).

| Item                                                                                                    | Owner              | When                |
|---------------------------------------------------------------------------------------------------------|--------------------|---------------------|
| Coordinate with ADR-080 owner on PR-0 (rate limiter) — confirm not duplicating their primitive          | schedd (you)       | Before PR-0         |
| Open issue: "ADR-099 jobs cluster" — links to all 7 PRs, tracks plan + status                           | you                | PR-A opens          |
| Financial-model reconciliation: `JobMaxPerAccount = [0,10,50,200]` vs `ex44_faas_financial_model.xlsx`. Update ADR-099 if model disagrees. | you                | Before PR-D ships   |
| Tracking issue for staged rollout: Hobby plan opt-in flag (`FAAS_JOBS_Hobby=true` first 2 weeks)         | you                | PR-D                |
| Internal announcement: docs site post "Jobs are here" + dashboard banner                                | docs / dashboard   | After PR-E merges   |
| PR-cluster-outline file (per ADR-098's `098-pr-cluster-outline.md` pattern)                              | you                | PR-A opens          |
| Update `pkg/daemonunitspec/` if jobs touches any daemon unit (it doesn't — schedd already exists)        | n/a                | n/a                 |

## Risk register (carried from ADR-099 + new findings)

1. **PR-0 must ship before PR-C.** Rate-limiter is load-bearing.
   Same primitive serves ADR-080. Coordinate.
2. **PR-A is a 4-migration PR.** Slot fences 00231–00240 are reserved
   so PR-B/C/D/E can race with other in-flight clusters. The fence
   pattern (`select 1;` with claim comment) avoids the renumber-chain
   dance. **Do not consume 00235–00240 in PR-A** — those are PR-B/C/D/E/F
   coordination headroom.
3. **ERRATUM-2 (`usage_minutes.app_id` nullable)** is the most
   consequential schema change. Backfill in migration 00234 sets
   `meter_kind='app'` on every existing row. Plan-roll-up queries
   in `meterd` and the dashboard must be re-read; pre-existing
   NULL-row assumptions could break. PR-A includes a `meterd`
   smoke test.
4. **Guest-init branch (`kind='job'`)** is a static-binary change.
   `make metal-lima` (per CLAUDE.md "default local loop for anything
   touching pkg/fcvm") is the dev iteration. The Lima guest is arm64
   but the production control-plane nodes are x86_64 — a green
   `make metal-lima` is necessary, not sufficient; bare-metal
   x86_64 sign-off remains required for §14 metal acceptance gates.
5. **Cross-PR slot races.** Per memory `cross-pr-slot-gate-races-with-active-pr`:
   run `git ls-tree origin/main migrations/` + `gh pr list --state open
   --json files` immediately before opening PR-A. If a fence is
   taken, renumber per the `cross-pr-slot-gate-reservation-fence-pattern`
   precedent.
6. **Spec drift gate.** ADR-085: `pkg/apid/openapi.yaml` is `//go:embed`;
   `make spec-sync` after every `pkg/api/spec/openapi.yaml` change.
   PR-D must `make spec-sync` clean before merge.
7. **Acceptance test pull-forward.** PR-E's `pkg/e2e/jobs_e2e_test.go`
   must cover RAM-cap rejection, rate-limit interaction, and
   dead-letter under retries. ADR-099 §Acceptance has 14 items;
   each maps 1:1 to a test in PR-E.

## Acceptance gates per PR

| PR    | Gate                                                                                  |
|-------|---------------------------------------------------------------------------------------|
| PR-0  | `make test`, `make lint`, `RateLimit_BurstThrottle`, `RateLimit_PerAccountIsolation` |
| PR-A  | `make test`, `make leakcheck`, `apply_walk_test` pins, slot-precheck script clean      |
| PR-B  | `make test`, `make lint`, every ADR-099 §Decision 2 transition covered                |
| PR-C  | `make test`, `make test-metal`, `make leakcheck` — VM lifecycle touched               |
| PR-D  | `make test`, `make spec-sync`, `make lint`, `spec-check` CI green                      |
| PR-E  | `make test`, `make test-metal`, `make leakcheck`, `make lint`                          |
| PR-F  | `make lint`, `make spec-check`, `docs-site` local preview                            |

## Open questions (resolve before PR-D)

1. **Job log retention beyond 30 days** — ADR-099 says reuse the
   existing `pkg/retention` shape. Confirm with operator side whether
   the 30-day default holds for jobs or if a 7-day default is more
   appropriate (jobs are typically not customer-facing; less retention
   burden).
2. **Per-account concurrent cap** — ADR-099 §Decision 5 caps parallelism
   per run, not per account. A misbehaving customer could pin 100 tasks
   × N runs simultaneously. Need to decide: cap per-account concurrent
   tasks separately, or accept the per-account RAM ceiling as the
   natural brake.
3. **Job image source** — `jobs.image_ref` is digest-pinned like
   `deployments.image_ref`. Does the same registry auth / digest-pinning
   machinery cover it, or do jobs need a separate `pkg/imaged` path?
   (My read: same, because the build pipeline produces the same OCI
   layout; jobs just reference it.)
4. **Reaper backstop for OOM-stuck tasks** — if a job task spins at
   100% CPU and never exits, the watchdog (`task_timeout_s`) is the
   only kill. Is the watchdog reliable under guest-init failure modes?
   Need to confirm with `pkg/fcvm` (`pkg/fcvm/manager.go:2689`
   `DestroyWithExport` path) that an unresponsive guest can be
   force-killed from the host side.

## Status

Draft 2026-08-13. PR-0 + PR-A can start once the ADR-080 owner
confirms coordination. PR-A opens the tracking issue + PR-cluster-outline
file.