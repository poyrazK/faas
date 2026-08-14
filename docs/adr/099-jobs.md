# ADR-099 · Jobs (run-to-completion workloads) — v1

- **Status:** proposed
- **Date:** 2026-08-13
- **Closes:** the long-standing "Cloud Run-style jobs" feature request.
  Gregale's wake model is request/response (ADR-005, §6). Customers
  running migrations, batch image-processing, periodic ETL, or one-off
  scripts today must either fold the work into a cron-tick against a
  live app or run it outside Gregale. This ADR introduces a separate
  `jobs` entity: a stored image + command + env that the platform runs
  to completion on demand, with retries, parallelism, timeouts, and
  task-level observability.

## Context

Real workloads need "run this image once and tell me the exit code."
Cloud Run jobs (`gcloud run jobs execute`), GitHub Actions, Fly
Machines, Heroku one-off dynos all serve this niche. Gregale has the
primitives — `pkg/fcvm` ephemeral VM, `pkg/fcvm.DestroyWithExport`
exit-code capture (already used by `builderd`), `pkg/meter`, the
`cron.fired` audit + dispatch plumbing — but no entity model.

Two adjacent proposals exist that this ADR must distinguish from:

- **ADR-080 (per-app async task queue, issue #668, status: proposed).**
  Enqueues work *for an existing app*, delivered as a synthetic HTTP
  wake with an internal Bearer token. Customer's handler returns
  2xx/5xx/4xx; the platform retries or dead-letters.
- **ADR-003 (builds in ephemeral builder microVMs).** The
  `builderd` pipeline already runs an image to completion inside a
  jailer-scoped FC microVM with a 10-min hard cap. The shape matches;
  the slot accounting does not — builder slots are deliberately
  scarce (1 guaranteed + 1 opportunistic per §13) because builds
  must never outrank tenant wakes.

Jobs sit between these two: the VM lifecycle is closer to a builder
VM (run to completion, capture exit code, no idle reaper) but the
admission goes through the tenant RAM ceiling, not the builder slot
pool — because job runs are tenant work, not platform work.

## Decisions

### 1. `jobs` is a separate entity from `apps`

Three new tables. `jobs` is the template (image ref, command, env,
limits); `job_runs` is one execution; `job_tasks` is one
(task-index, run) pair.

```sql
CREATE TABLE public.jobs (
    id              bigserial PRIMARY KEY,
    account_id      bigint NOT NULL REFERENCES public.accounts(id) ON DELETE CASCADE,
    name            text NOT NULL,
    image_ref       text NOT NULL,            -- OCI digest-pinned, same shape as deployments.image_ref
    command         text[] NOT NULL DEFAULT '{}',
    env             jsonb NOT NULL DEFAULT '{}'::jsonb,
    parallelism     int  NOT NULL DEFAULT 1
                    CHECK (parallelism BETWEEN 1 AND 100),
    max_attempts    int  NOT NULL DEFAULT 1
                    CHECK (max_attempts BETWEEN 1 AND 10),
    task_timeout_s  int  NOT NULL DEFAULT 600
                    CHECK (task_timeout_s BETWEEN 10 AND 3600),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (account_id, name)
);

CREATE TABLE public.job_runs (
    id              bigserial PRIMARY KEY,
    job_id          bigint NOT NULL REFERENCES public.jobs(id) ON DELETE CASCADE,
    account_id      bigint NOT NULL REFERENCES public.accounts(id) ON DELETE CASCADE,
    triggered_by    text NOT NULL,            -- 'manual' | 'schedule' | 'api'
    actor           text,                     -- account email or 'system'
    task_count      int NOT NULL,
    parallelism     int NOT NULL,
    env_override    jsonb NOT NULL DEFAULT '{}'::jsonb,
    status          text NOT NULL DEFAULT 'queued'
                    CHECK (status IN ('queued','running','succeeded','failed','cancelled','dead_letter')),
    started_at      timestamptz,
    finished_at     timestamptz,
    audit_id        bigint REFERENCES public.audit_events(id)
);

CREATE TABLE public.job_tasks (
    id              bigserial PRIMARY KEY,
    run_id          bigint NOT NULL REFERENCES public.job_runs(id) ON DELETE CASCADE,
    job_id          bigint NOT NULL REFERENCES public.jobs(id),
    task_index      int NOT NULL,
    attempt         int NOT NULL DEFAULT 1,
    status          text NOT NULL DEFAULT 'queued'
                    CHECK (status IN ('queued','running','succeeded','failed','timeout','dead_letter')),
    instance_id     uuid REFERENCES public.instances(id),  -- NULL until admitted
    scheduled_for   timestamptz NOT NULL DEFAULT now(),
    started_at      timestamptz,
    finished_at     timestamptz,
    exit_code       int,
    error_kind      text,                     -- 'oom' | 'watchdog' | 'guest_init' | 'exit_nonzero'
    UNIQUE (run_id, task_index, attempt)
);

CREATE INDEX job_tasks_ready_idx ON public.job_tasks (scheduled_for)
    WHERE status = 'queued';
CREATE INDEX job_runs_account_idx ON public.job_runs (account_id, started_at DESC);
```

The `job_tasks.instance_id` FK is the seam to `pkg/sched` —
`job_tasks.instance_id` IS the wake VM. See §Decision 3.

### 2. State machine mirrors ADR-080 with run-level fan-in

Six states on `job_tasks.status`, seven transitions:

| From       | Trigger                                       | To            | Side effects                                              |
|------------|-----------------------------------------------|---------------|-----------------------------------------------------------|
| (insert)   | run INSERT                                    | `queued`      | `scheduled_for = now()`, `pg_notify('job_tasks_queued', …)` |
| `queued`   | `schedd` claims row (`FOR UPDATE SKIP LOCKED`) | `running`     | `instance_id` set, `started_at = now()`, `audit.job.task.dispatched` |
| `running`  | exit code 0                                   | `succeeded`   | `audit.job.task.succeeded`, `finished_at = now()`         |
| `running`  | exit code != 0                                | `failed`      | if `attempt < max_attempts` → reschedule `queued`; else `dead_letter` |
| `running`  | watchdog (10 min / task_timeout_s)            | `timeout`     | same retry rule as `failed`                               |
| `running`  | OOM-killed by cgroup                          | `failed` with `error_kind='oom'` | same retry rule                            |
| any        | `POST /v1/jobs/{name}/runs/{id}/cancel`       | `cancelled`   | `audit.job.task.cancelled`, guest-init SIGTERM            |

The `job_runs.status` aggregate is computed by `schedd` on every
`job_tasks` transition (no separate writer): `succeeded` iff every
task reached `succeeded`; `failed` iff at least one reached
`dead_letter` or `timeout` after exhaustion; `running` otherwise;
`cancelled` iff any task was `cancelled`.

### 3. Job tasks ride the wake-VM path with `instances.kind = 'job_task'`

A new CHECK value on `instances.kind`. The state machine (§6) is
unchanged; the reaper (§6.1) gains one branch:

```sql
ALTER TABLE public.instances
    DROP CONSTRAINT instances_kind_check,
    ADD  CONSTRAINT instances_kind_check
         CHECK (kind IN ('wake', 'build', 'job_task'));
```

Reaper rule for `kind = 'job_task'`:

- **No idle timeout.** Job tasks run until guest-init SIGTERMs the
  entrypoint (exit-code capture path) or the watchdog fires.
- **No `min_instances` floor.** Job tasks are not part of the
  per-app RAM pool R1 invariant (§6.2 §2); they are billed as
  one-off metered seconds (see §Decision 7).
- **Park on `guest-init` exit-code DGRAM**, not on idle. The exit
  code is captured via the existing `pkg/fcvm.DestroyWithExport`
  path used by `builderd` — zero new VM plumbing.

This is the load-bearing reuse: jobs take the wake-VM path (jailer,
cgroup, snapshot restore) but bypass the wake's idle-reaper because
their lifecycle is bounded by `task_timeout_s`, not by traffic.

### 4. Admission goes through the tenant RAM ceiling, NOT the builder slot pool

`schedd.EnsureInstance(kind=job_task, ram_mb=job.ram_mb)` consults the
existing R1 ledger (`pkg/sched/engine.go::admitGate`) just like a
wake. A Scale customer with 100 concurrent job tasks at 1024 MB each
costs 100 GB-seconds/s — capped by `MaxRAMMB = 47,600`. The
overshoot is rejected with `CodeAdmissionRefused` (402, same shape as
the existing cap-hit gate).

This preserves the §13 rule: **builds never outrank tenant wakes**.
Jobs are tenant work and ride the tenant admission. They do not
consume the scarce `builderd` slot.

A Scale customer fanning out 1000 tasks at once triggers the
existing `RateLimitRPS`/`RateLimitBurst` per-app limiter, which is
NOT enough. See §Risk #1: ADR-080 already identifies this gap and
calls for a `pkg/sched/rate_limit.go::EnsureInstanceRateLimit(appID,
burstTo int)` token-bucket-per-app primitive. That pre-PR ships
alongside this ADR — same primitive serves both task queues and
jobs. Per-plan burst ceiling: Free 1 / Hobby 5 / Pro 20 / Scale 100
wakes/min.

### 5. Plan-bound caps in `pkg/api/limits.go`

```go
const (
    JobMaxPerAccount        = [4]int{0, 10, 50, 200}      // Free 0 / Hobby / Pro / Scale
    JobMaxParallelismPerRun = [4]int{1, 5, 20, 100}
    JobMaxTasksPerRun       = [4]int{10, 100, 1000, 10000} // matches Cloud Run 10k ceiling
    JobMaxAttemptsPerTask   = [4]int{1, 3, 5, 10}
    JobTaskTimeoutMaxSec    = [4]int{600, 1800, 3600, 3600}
    JobBackoffBaseSeconds   = 5
    JobBackoffMaxSeconds    = 5 * 60
)
```

Free plan returns 402 `jobs_not_allowed` on `POST /v1/jobs`. The cap
enforcement mirrors `CronLimitPerApp` — single source of truth in
`Limits`, enforced in `apid` at accept time and re-checked in
`schedd` on every dispatch tick.

### 6. `schedd` owns dispatch via a sibling cron-style tick

`pkg/sched/dispatch_jobs.go::runJobsTick` mirrors
`runCronTick` (sibling to `runTasksTick` from ADR-080). On
`LISTEN job_tasks_queued` and on a 1 s wall-clock tick:

1. Compute the per-account remaining admission budget
   (47,600 MB - Σ live instance RAM).
2. For each `job_tasks` row in `queued` ordered by `(scheduled_for,
   task_index)`, claim with `FOR UPDATE SKIP LOCKED`, consult
   `EnsureInstanceRateLimit`, then `schedd.EnsureInstance(kind=
   'job_task', image=jobs.image_ref, ram_mb=plan.RAMMB,
   timeout=jobs.task_timeout_s)`.
3. On wake-ready DGRAM (existing `framework_ready` seam), set
   `job_tasks.instance_id` and emit `audit.job.task.dispatched`.
4. On exit-code DGRAM (new port, see §Decision 8), transition the
   row per the §Decision 2 state machine.

Parallelism is enforced by counting running tasks per `(job_id,
run_id)` and refusing to dispatch more until one finishes. The 1 s
tick is the throughput ceiling: with the per-app rate-limit at
Scale=100/min, a 100-task fan-out drains in ~60 s wall-clock.

### 7. Metering: continuous RAM-seconds from wake to exit

Jobs reuse `pkg/meter` directly. The existing `usage_minutes` row
shape accepts `(account_id, app_id=NULL, instance_id, mb_seconds,
started_at, ended_at)` — `app_id` is nullable today; jobs write
`app_id=NULL, instance_id=job_tasks.instance_id, kind='job'`. A new
partial index `usage_minutes_job_idx` makes the per-account / per-job
roll-up query cheap.

Billing: jobs bill the same way as wakes — `ram_mb × seconds` against
plan limits and overage. There is no separate jobs SKU in v1. v1.1
may add a per-task flat fee; out of scope here.

### 8. Guest-init change: `kind = 'job'` in `app.json`

`guest/init/app.go` already parses `kind` from `/etc/faas/app.json`.
Adding `'job'` is a branch in the supervisor:

- `kind = 'app'` (today): spawn the long-lived listener process,
  enforce `snapshot_and_park` watchdog.
- `kind = 'job'` (new): spawn `command[0]` with `command[1..]`
  and the merged env, capture exit code, write a one-shot
  `job_task.exit` DGRAM to the host (`port=1026, msg_type=3`,
  distinct from `VsockResumePort=1024` and the characterize
  probe's `1025/2`), then `os.Exit(code)`.

The watchdog for `kind = 'job'` is `task_timeout_s` (per-job),
not the wake's 5 s `snapshot_and_park`. On timeout, guest-init
SIGTERMs the entrypoint and writes `error_kind='timeout'`. This
is the same exit-code DGRAM path `builderd` already uses via
`pkg/fcvm.DestroyWithExport`.

### 9. CLI surface (`cmd/gregale`)

```bash
gregale job create <name> --image <ref> --command "migrate --to v2"
gregale job list
gregale job get <name>
gregale job run <name> [--tasks 100] [--parallelism 10] [--env K=V]...
gregale job runs <name>                       # list recent runs
gregale job logs <name> [--run <id>] [--task <n>] [--follow]
gregale job cancel <run-id>
```

CLI shape mirrors `cmd/gregale`'s `crons` subcommand (which already
exists per ADR-090). HTTP-only — no direct DB imports (CLAUDE.md
"CLI is HTTP-only"). All methods typed in `pkg/api/client.go`.

### 10. SDK + OpenAPI

`pkg/api/openapi.yaml` gains `Job`, `JobRun`, `JobTask` schemas and
the eleven routes:

- `POST   /v1/jobs`
- `GET    /v1/jobs`
- `GET    /v1/jobs/{name}`
- `PATCH  /v1/jobs/{name}`
- `DELETE /v1/jobs/{name}`
- `POST   /v1/jobs/{name}/runs`
- `GET    /v1/jobs/{name}/runs`
- `GET    /v1/jobs/{name}/runs/{id}`
- `POST   /v1/jobs/{name}/runs/{id}/cancel`
- `GET    /v1/jobs/{name}/runs/{id}/tasks`
- `GET    /v1/jobs/{name}/runs/{id}/logs`

`pkg/api/spec-sync` regenerates `pkg/apid/openapi.yaml` and the
three SDKs. `make spec-sync` is the gate (per ADR-085).

## Consequences

### Files added

- `migrations/00NN_jobs.sql` (goose, append-only) — three tables,
  the `instances_kind_check` widening, the `usage_minutes_job_idx`
  partial index.
- `pkg/state/jobs.go` — sqlc-generated CRUD (CreateJob, GetJob,
  ListJobs, CreateRun, ClaimTask, MarkTaskSucceeded, MarkTaskFailed,
  MarkTaskTimeout, MarkRunStatus, etc.).
- `pkg/sched/dispatch_jobs.go` — `runJobsTick`, `LISTEN
  job_tasks_queued`, the parallelism cap, the rate-limit check, the
  exit-code DGRAM handler.
- `pkg/sched/rate_limit.go` — the per-app token bucket (the
  pre-PR primitive also consumed by ADR-080 once it lands).
- `pkg/sched/jobs.go` — `Engine.AllocateJobTask` /
  `Engine.MarkJobTaskTerminal` helpers.
- `guest/init/job_supervisor.go` — the `kind = 'job'` branch.
- `pkg/api/limits.go` — the 7 plan-bound constants in §Decision 5.
- `pkg/wire/metrics/jobs.go` — `jobs_runs_total{job,plan,status}`,
  `jobs_tasks_total{job,plan,status,attempt}`,
  `job_task_duration_seconds{job,plan,outcome}` histogram,
  `jobs_queue_depth{job,plan}` gauge,
  `jobs_concurrent{account}` gauge,
  `jobs_dispatch_seconds` histogram.
- `cmd/apid/handlers/jobs.go` — the 11 routes in §Decision 10.
- `pkg/events/job.go` — event kinds `job.created`, `job.run.queued`,
  `job.run.started`, `job.run.finished`, `job.task.dispatched`,
  `job.task.succeeded`, `job.task.failed`, `job.task.timeout`,
  `job.task.dead_letter`, `job.task.cancelled`.
- `sdk/{go,node,python}/...` — typed client methods.
- `cmd/gregale/cmd/jobs.go` — the 7 CLI subcommands.
- `pkg/e2e/jobs_e2e_test.go` — full lifecycle (create, run, fan-out,
  retry, cancel, dead-letter, exit-code capture, RAM cap, Free
  plan rejection).
- `pkg/api/spec/openapi.yaml` — schemas + routes.

### Files modified

- `pkg/api/limits.go` — the 7 constants.
- `pkg/state/pgstore.go` — `instances_kind_check` migration hooks.
- `pkg/sched/engine.go::EnsureInstance` — accept `kind='job_task'`,
  bypass the idle-reaper path for it.
- `pkg/sched/loop.go::Loop` struct — register `runJobsTick` alongside
  `runCronTick` and the proposed `runTasksTick` (ADR-080).
- `cmd/schedd/main.go` — wire the new tick + the per-app rate
  limiter.
- `guest/init/app.go` — branch on `kind` to dispatch to the
  long-lived listener or the new `job_supervisor`.
- `docs/faas_implementation_spec.md` §4.7.4 — new paragraph on
  jobs; §6.1 — reaper rule for `kind='job_task'`.
- `docs/STATUS.md` M7.x entry — pin e2e test to wire surface.

### Operational signals

- `jobs_queue_depth{job,plan}` — backlog gauge.
- `jobs_concurrent{account}` — instantaneous concurrent tasks; alert
  at `> 0.8 × plan_cap` for 5 min.
- `job_task_duration_seconds` p99 — primary SLO metric. Target
  p99 < 10 min (cap), p50 < 30 s for typical workloads.
- `jobs_dispatch_seconds` p99 — wall-clock from `pg_notify` to
  `framework_ready`. Alert at > 5 s (cold boot dominates; warm
  wake target < 1 s).
- `jobs_dispatch_rejected_total{plan,reason}` — reasons:
  `ram_cap`, `rate_limit`, `parallelism_cap`, `free_plan`.

### Acceptance

- [ ] `gregale job run migrate --tasks 100 --parallelism 10`
      returns a `run_id` within 100 ms p50 (warm Postgres).
- [ ] Tasks dispatch within 1 s of enqueue (idle instance) or via
      the existing cold-boot wake path.
- [ ] Exit code 0 marks `succeeded`; non-zero marks `failed` and
      retries up to `JobMaxAttemptsPerTask`; after exhaustion the
      task flips to `dead_letter`.
- [ ] OOM-killed tasks retry with `error_kind='oom'` and backoff
      `JobBackoffBaseSeconds × 2^(attempt-1)`, capped at
      `JobBackoffMaxSeconds`.
- [ ] `task_timeout_s` watchdog flips to `timeout` and retries
      with backoff.
- [ ] `cancel` writes `audit.job.task.cancelled` and SIGTERMs
      the guest within 1 s.
- [ ] Per-account RAM cap (47,600 MB aggregate with live app
      instances) returns `CodeAdmissionRefused`.
- [ ] Per-app wake rate-limit (`Scale=100/min`) returns
      `rate_limit_exceeded` and the task stays `queued`.
- [ ] `job run --tasks 10000 --parallelism 100` on Scale plan
      drains within ~10 min wall-clock at the dispatch SLO.
- [ ] Free plan returns 402 `jobs_not_allowed` on `POST /v1/jobs`.
- [ ] `gregale job logs <name> --follow` streams task stdout via
      the existing `app_logs` SSE channel.
- [ ] `pkg/e2e/jobs_e2e_test.go` exercises the full lifecycle.
- [ ] All plan-bound caps round-trip through `pkg/api/limits.go`.
- [ ] `pkg/state/jobs_test.go` covers every state transition in
      §Decision 2.
- [ ] Dashboard renders `job.run.finished` rows in the run
      history view.

### Implementation notes (mega-PR landed; deltas vs the planned file list)

The v1 cluster shipped as a single mega-PR on
`worktree-jobs-pr-b-state` consolidating PR-0 (rate-limit
separation) → PR-A (schema, §6 widening) → PR-B (pkg/state CRUD
+ sqlc) → PR-C (schedd `runJobsTick` + `Engine.WakeJob` +
vsock `job_exit` decode + guest-init supervisor + reaper kind
filter) → PR-D (apid handlers + OpenAPI + SDK regen) → PR-E
(CLI + e2e + harness migration head) → PR-F (this section,
spec §4.7.4, runbooks, STATUS). As-built deltas:

- **Wake path factoring (§Decision 9 carrier)**. `Engine.Wake`
  body was extracted into `Engine.bootInstance` so both the
  app-wake and `Engine.WakeJob` paths share the
  VM-creation harness. The app path keeps the snapshot
  restore; the job path skips `SnapLoad=Yes` and uses
  `instanceKind='job_task'` to drive the cgroup scope +
  reaper filter. The migration that added `instances.kind`
  CHECK has a side-effect of FORCING every existing writer to
  stamp `'app'` explicitly — that was the intended
  consequence and landed cleanly.
- **Vsock discriminator (§Decision 9 carry-over)**. `port=1026`
  carries two `msg_type`s: `3` (pre-existing characterization)
  and `4` (new `job_exit`). Both share the conn but the
  `msg_type` byte disambiguates. `pkg/fcvm/vmm.go::WaitJobExit`
  is the new entry point; the legacy `WaitCharacterization`
  (msg_type=3) is unchanged.
- **CLI location** (§Decision 9 originally cited
  `cmd/gregale/cmd/jobs.go`). Corrected to
  `cmd/gregale/commands_jobs.go` — `cmd/gregale` is the package
  root; no `cmd/` subdirectory exists.
- **E2E harness migration head** (§Decision 5). `pkg/e2etest/harness.go`
  bumps `e2eMigrationTarget` from 237 → 264 over the merge;
  the seams that matter: FAAS_JOBS_ENABLED=1 stamped on the
  harness, `APID | Meterd` boot (no schedd/vmmd — those need
  metal). The Free-plan gate (404 jobs_not_allowed) is
  exercised in `TestJobsCRUDMatrixPg`; the Hobby cap
  (= 5 in `pkg/api/limits.go::PlanHobby.JobMaxPerAccount`) is
  exercised in `TestJobRunQuotaBreach`.
- **kind naming**: `instances.kind='job'` is the wire value
  (the plan picked `'job'` for the column discriminator). The
  SCHEDULER-side `Kind` carrier uses the same vocabulary
  (`wake | build | job_task`); `job_task` is the schedd path
  label, `job` is the wire label — the carrier↔wire map is in
  `pkg/sched/loop.go::kindForInstance`. ADR-099 §Decision 9
  pin: a future name change is an ADR-bound decision, not a
  free refactor.
- **Spec drift**: §6 (state machine) and §4.7.4 (this
  ADR's v1 appendage) updated in the same PR cluster as
  the `instances_kind_check` migration. The CI spec-sync
  drift gate would otherwise fail.

### Out of scope landed as deferred (separate ADRs required)

- **meterd rollup widening** — `usage_minutes.meter_kind='job'`
  rows are flushed per-minute but not yet rolled up to
  `usage_daily` against the `(account_id, NULL, day)` bucket.
  Functions correctly for billing (per-second `mb_seconds`
  meter is correct), but dashboards show NULL-app-id daily
  rows. Tracking issue: separate ADR.
- **jobs_minutes_metered_total Prometheus metric** —
  referenced in this ADR §Decision 10 / Acceptance but the
  wiring belongs in a meterd follow-up PR.

### Risk register

1. **Cold-boot wake-storm on task fan-out** (mirrors ADR-080
   Risk #1). A Scale customer running `--tasks 1000 --parallelism
   100` against a parked app triggers 100 cold boots in one
   `runJobsTick`. Mitigation: the **per-app wake rate-limiter
   primitive** (§Decision 4) is a load-bearing pre-requisite. The
   primitive ships in `pkg/sched/rate_limit.go` as a sibling PR to
   the migration; the rate-limit check is in the v1 dispatch path
   so this ADR cannot ship green without it.
2. **Builder slot pressure is unchanged.** Jobs do not consume
   builder slots (§Decision 4). Confirmed by reading
   `pkg/sched/engine.go::admitGate` — the R1 ledger treats
   `kind='job_task'` the same as `kind='wake'` for admission.
3. **Exit-code DGRAM port collision.** The new `port=1026,
   msg_type=3` (`job_task.exit`) must be reserved alongside
   `VsockResumePort=1024` and the characterize probe's `1025/2`
   in `pkg/fcvm/vsock.go` (or its current equivalent). Pin in
   the same PR.
4. **`usage_minutes` aggregation.** Today `meterd` rolls up
   `usage_minutes` per `(account_id, app_id)`. Job tasks carry
   `app_id=NULL`; `meterd` must accept the NULL-app_id case and
   expose per-job roll-ups via a new `meter_kind` discriminator.
   PR-1 of this ADR includes the `meterd` change.
5. **CLI `tasks` keyword collision.** `gregale job run <name>
   --tasks N` must not be confused with ADR-080's
   `gregale tasks enqueue`. The `job` subcommand owns
   `--tasks`; the `tasks` subcommand owns positional payload.
   Documented in `cmd/gregale/cmd/jobs.go` header.
6. **Spec drift.** §6 of the spec must be updated in the same PR
   as the `instances_kind_check` migration; `pkg/sched/loop.go`
   comments refer to "wake or build" — those become "wake, build,
   or job_task". CI fails the merge otherwise (per the
   spec-sync drift gate from ADR-085).

### Out of scope (explicit, v1.1+)

- **Scheduled / cron-fired runs** — `jobs.cron_schedule` field
  with the existing cron evaluation pipeline. v1 is manual +
  API-triggered only.
- **Per-task env overrides at the CLI** — v1 supports one
  `env_override` per `job_runs` row (applied to all tasks in
  the run). Per-task override (e.g. `TASK_INDEX=42`) is a v1.1
  follow-up — Cloud Run models `TASK_INDEX` / `TASK_ATTEMPT`
  env vars; we will mirror that shape once the run-level env is
  proven.
- **Cross-run dependencies / DAGs** — rejected per "start small"
  guidance. Workflow primitives (ADR-080 §"Out of scope" + #669)
  stay separate.
- **GPU / large-memory job plans** — `JobTaskTimeoutMaxSec` caps
  at 3600 s (1 h) in v1. Cloud Run jobs cap at 24 h; we may
  extend once the financial model budgets for it (see
  `ex44_faas_financial_model.xlsx`).
- **Webhook delivery on run completion** — v1 emits
  `job.run.finished` SSE; HTTP webhook fan-out is ADR-076's
  territory.
- **Per-job log retention beyond 30 days** — reuses the existing
  `pkg/retention` shape; no new policy.
