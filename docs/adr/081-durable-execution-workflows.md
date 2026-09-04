# ADR-081 · Durable-execution wrapper over crons (issue #669)

- **Status:** proposed
- **Superseded (in part, PR-E):** prose referred to the monolithic
  `cmd/gatewayd/` daemon split by ADR-070 into `gatewayd-public` (TLS-only
  edge) and `gatewayd-internal` (routing + wake + proxy). Body is preserved
  verbatim; readers should substitute "gatewayd-internal" for the
  routing/wake/proxy path and "gatewayd-public" for the certmagic/TLS path.
  `cmd/gatewayd/<file>.go` citations in this body are stale; see PR-E for
  the new file locations.
- **Date:** 2026-08-07
- **Closes:** #669
- **Decision:** Add a per-app **durable-execution workflow** primitive —
  a thin declarative DSL over the existing cron synthetic-wake path
  and Postgres state. Customers declare a `workflows:` block (steps
  + `depends_on` + retry + `wait_for_event`) in the deploy payload;
  the platform owns the state machine, the retry/backoff, the step
  audit log, and the external-event primitive. State lives in
  Postgres (`public.workflow_runs`, `public.workflow_steps`,
  `public.workflow_events`); execution is one synthetic wake per
  step. Free plan disabled (Hobby+). Explicitly **not** a Temporal
  / Inngest / Restate competitor — no deterministic replay, no
  code-as-workflow, no versioning, no signals/queries.
- **Why:** Every real app has multi-step workflows (sign-up →
  verify email → provision tenant → send welcome), but Gregale's
  only durable primitive today is "chain crons." Customers hand-roll
  the state machine, the retry logic, the audit log, and the
  external-event primitive — the 80% case is durable execution on a
  FaaS platform, but Temporal / Inngest / Restate require a control
  plane Gregale does not have and **should not** build (different
  product, different margin). The realistic play is a **thin
  framework** that hides the cron-chain pattern with a small
  declarative DSL over the existing surface. **80% of the value at
  5% of the complexity** of a real Temporal competitor — and the
  same wiring ADR-080 (issue #668, task queue) just proposed.
- **Issue:** #669

## Context

Gregale's wake model is **request-driven** today. The crons primitive
extends it to scheduled synthetic wakes — but crons only fire a
single step. A multi-step workflow requires the customer to chain
crons: cron #1 fires a request, the handler writes a
`workflows`-shaped row to Postgres with the next step's
`scheduled_for`, cron #2 fires later, etc. This works, but the
customer owns:

- The state machine (5 states: pending / running / awaiting_event /
  succeeded / failed / dead, plus per-step status).
- The retry / backoff / dead-letter logic.
- The idempotency / exactly-once semantics (the customer must make
  each step idempotent because retries re-execute).
- The step history (audit log per step).
- The "wait for an external event" primitive (typically a
  long-polling cron, which burns wakes).

This is the **80% case** for durable execution. **Temporal /
Inngest / Restate** ship the full 100% (replay, deterministic
code-as-workflow, versioning, signals, queries) — but they require
a control plane that Gregale does not have and **should not**
build. Different product, different margin.

### Why now, not later

ADR-080 (issue #668, task queue) just shipped as a proposal. The
two threads are siblings — both are STATELESS primitives that reuse
the cron synthetic-wake path. ADR-081 explicitly **shares the
synthetic-wake Bearer primitive** with ADR-080 (see §"Sequencing"
below). Landing them as a pair gives customers both
fire-and-forget (tasks) and durable-execution (workflows) at the
same time, both running on the same shared wake path.

### Non-goals (explicit per #669 §"Out of scope")

- Code-as-workflow / deterministic replay — point at Temporal /
  Inngest / Restate.
- Workflow versioning — today: a new deploy starts new runs;
  in-flight runs complete against the old definition; no
  migration.
- Cross-app workflows — single-app workflows only.
- Workflow signal / query primitives (Temporal-style) — use the
  `events` table directly via a sidecar if needed.
- Persistent state across wakes — rejected per #669 §"Out of scope"
  (breaks the niche).

## Decisions

### 1. State lives in Postgres

Three new tables:

```sql
CREATE TABLE public.workflow_runs (
    id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id                   bigint NOT NULL REFERENCES public.apps(id) ON DELETE CASCADE,
    workflow_name            text NOT NULL,
    status                   text NOT NULL DEFAULT 'pending'
                             CHECK (status IN ('pending','running','awaiting_event',
                                              'succeeded','failed','dead')),
    current_step             text,
    input                    jsonb NOT NULL,
    output                   jsonb,
    definition_snapshot      jsonb NOT NULL,       -- see §"Consequences — versioning"
    started_at               timestamptz NOT NULL DEFAULT now(),
    finished_at              timestamptz,
    last_error               text,
    audit_id                 bigint REFERENCES public.audit_events(id)
);

CREATE TABLE public.workflow_steps (
    run_id          uuid NOT NULL REFERENCES public.workflow_runs(id) ON DELETE CASCADE,
    step_name       text NOT NULL,
    status          text NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending','running','awaiting_event',
                                       'succeeded','failed','dead','skipped')),
    attempt         int NOT NULL DEFAULT 0,
    input           jsonb,
    output          jsonb,
    started_at      timestamptz,
    finished_at     timestamptz,
    error           text,
    PRIMARY KEY (run_id, step_name)
);

CREATE TABLE public.workflow_events (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id       uuid NOT NULL REFERENCES public.workflow_runs(id) ON DELETE CASCADE,
    event_name   text NOT NULL,        -- matches wait_for_event
    payload      jsonb NOT NULL,
    received_at  timestamptz NOT NULL DEFAULT now()
);

-- Partial index on the dispatch hot path. The pg_notify trigger
-- below fires only when status='pending' or status='running',
-- so queued rows are the hot path; the partial index keeps the
-- dispatch SELECT cheap.
CREATE INDEX workflow_runs_dispatch_idx
    ON public.workflow_runs (scheduled_for)
    WHERE status IN ('pending','running','awaiting_event');

-- pg_notify trigger for the schedd dispatch tick. Mirrors
-- migrations/00031_invocations_notify.sql:21-60.
CREATE OR REPLACE FUNCTION workflow_due_notify() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE payload jsonb;
BEGIN
    payload := jsonb_build_object(
        'run_id', new.id::text,
        'app_id', new.app_id::text,
        'workflow_name', new.workflow_name,
        'status', new.status
    );
    PERFORM pg_notify('workflow_due', payload::text);
    RETURN new;
END;
$$;

DROP TRIGGER IF EXISTS workflow_due_trg ON public.workflow_runs;
CREATE TRIGGER workflow_due_trg
    AFTER INSERT OR UPDATE OF status ON public.workflow_runs
    FOR EACH ROW
    WHEN (new.status IN ('pending','running','awaiting_event'))
    EXECUTE FUNCTION workflow_due_notify();
```

`workflow_runs.scheduled_for` is added (omitted in the snippet for
brevity, but the dispatch index references it — must be present in
the actual migration, default `now()`).

Why Postgres, not a new store:

- The control plane already runs Postgres with `pg_notify`; adding
  Redis Streams or a workflow engine means a new deployment unit, a
  new backup story, a new failure mode.
- The dispatch hot path is exactly the cron path's shape
  (`scheduled_for`-indexed partial index + `FOR UPDATE SKIP LOCKED`
  claim); reusing it is the whole point.

### 2. Execution is synthetic wake per step, reusing the cron path

Each workflow step is delivered as a synthetic wake exactly like a
cron fires today. The wake's handler receives:

```http
POST /_workflows/<run_id>/<step_name>
Authorization: Bearer <internal faas_task_…>
X-Faas-Workflow-Run-Id: <uuid>
X-Faas-Workflow-Step: <step_name>
X-Faas-Workflow-Attempt: 1

{ "input": { ... }, "context": { ... } }
```

A 2xx response advances the run to the next step. A 5xx retries
with exponential backoff up to the step's `max_attempts`. A 4xx
fails the step (the run continues if downstream steps have
`on_error: continue`, otherwise the run is `dead`).

The synthetic-wake RPC is the existing `GatewaySynth.Invoke` on
`pkg/sched/loop.go:1313` (the cron path crosses the
schedd→gatewayd boundary through this RPC; the new
`pkg/sched/dispatch_workflows.go::runWorkflowsTick` calls the
same interface). This is the **same shape** the future task-queue
PR (per ADR-080) uses.

### 3. DSL is declarative — JSON-shaped `WorkflowSpec` on the deploy DTO

The DSL ships as a sibling JSON field on `CreateDeploymentRequest`
(mirroring the existing `Sidecars` field at `pkg/api/dto.go:631`).
The same shape is available in the CLI's `gregale.yaml` manifest and is
converted to the JSON-tagged deploy shape. Duration values follow
`time.ParseDuration` with a fixed 24-hour `d` suffix for workflow examples
such as `7d`.

```jsonc
{
  "workflows": [
    {
      "name": "process_order",
      "trigger": { "type": "manual" },
      "steps": [
        { "name": "charge",
          "run": "charge_stripe",
          "input": { "order_id": "{{input.order_id}}" },
          "retry": { "max_attempts": 3, "backoff": "exponential" },
          "timeout": "30s"
        },
        { "name": "reserve_inventory",
          "run": "reserve_inventory",
          "depends_on": ["charge"],
          "retry": { "max_attempts": 5 }
        },
        { "name": "wait_for_shipment",
          "wait_for_event": "shipment_created",
          "timeout": "7d",
          "on_timeout": "refund_and_notify"
        },
        { "name": "finalize",
          "run": "mark_order_complete",
          "depends_on": ["wait_for_shipment"] }
      ]
    }
  ]
}
```

At deploy time, `apid` validates:

- No circular `depends_on` (topological sort succeeds).
- `wait_for_event` steps have no `run`.
- All `wait_for_event.timeouts` ≤ `WorkflowMaxWaitDays = 7`.
- All step timeouts ≤ `WorkflowStepMaxTimeout` for the plan.
- Free plan returns 402 `plan_workflows_not_allowed` at deploy time.

Each `workflow_runs` row stores `definition_snapshot` (a copy of
the resolved workflow spec at start time). The run reads its own
snapshot, never the live deploy — see §"Consequences — Versioning".

### 4. `wait_for_event` is the only long-park primitive

A `wait_for_event: shipment_created` step parks the run in
`status='awaiting_event'` until either:

- A `POST /v1/apps/{slug}/workflows/<run_id>/events/<event_name>`
  arrives. `apid` writes a row to `workflow_events` and emits
  `pg_notify('workflow_due', …)` for the schedd tick. The run is
  claimed, the matching step transitions `awaiting_event →
  running`, and the step's `on_event_received` handler fires (a
  synthetic wake with the event payload).
- The step's `timeout` elapses, triggering the `on_timeout`
  handler (a synthetic wake to the timeout-named step).

The run is **cheap to wait** — no VM is held, the row is just
sitting in `awaiting_event` until the event lands. This is the
Temporal signal pattern, implemented in 4 lines of SQL and a single
pg_notify.

### 5. Free plan disabled (Hobby+)

Free plan returns 402 `plan_workflows_not_allowed` on
`POST /v1/apps/{slug}/workflows/<name>/start` AND on any deploy
that includes a `workflows:` block. Aligns with the cron limit
shape (`cron_limit_per_app` per plan) and the ADR-080 task-queue
Free-disabled pattern.

The error pair follows the existing cron precedent
(`pkg/api/errors.go:1249` for the function, :1183-:1189 for the constants):

- `ErrPlanWorkflowsNotAllowed(p Plan) *Problem` → 402
- `ErrPlanWorkflowsQuota(p Plan, observed int) *Problem` → 403
  (plan unlocked, over the cap).

### 6. Plan-bound caps, single source of truth

```go
// pkg/api/limits.go (named struct fields, NOT [4]int arrays —
// see pkg/api/limits.go:39 for the Limits struct shape)
type Limits struct {
    // … existing fields …
    WorkflowMaxConcurrent   int           // concurrent runs per app
    WorkflowStepMaxTimeout  time.Duration // per-step ceiling
    WorkflowMaxWaitDays     int           // wait_for_event timeout cap
}

var planLimits = map[api.Plan]Limits{
    PlanFree:  { WorkflowMaxConcurrent: 0,    WorkflowStepMaxTimeout: 0,     WorkflowMaxWaitDays: 0 },
    PlanHobby: { WorkflowMaxConcurrent: 10,   WorkflowStepMaxTimeout: 10*time.Minute,  WorkflowMaxWaitDays: 7 },
    PlanPro:   { WorkflowMaxConcurrent: 50,   WorkflowStepMaxTimeout: 30*time.Minute,  WorkflowMaxWaitDays: 7 },
    PlanScale: { WorkflowMaxConcurrent: 200,  WorkflowStepMaxTimeout: 2*time.Hour,     WorkflowMaxWaitDays: 7 },
}
```

This is the **named struct field** pattern, NOT the ADR-080 prose
shorthand `[4]int{0, 10, 50, 200}` (which describes the values
inside `planLimits`, not the Go API). ADR-080's prose is a
documentation convenience; the actual code follows `Limits` + map.

The enforcer per cap:

- `WorkflowMaxConcurrent` → `apid` at run-start (returns 403
  `plan_workflows_quota`).
- `WorkflowStepMaxTimeout` → `apid` at deploy (rejects the deploy
  with 400); runtime step timeouts enforced via the
  `GatewaySynth.Invoke` per-request context deadline.
- `WorkflowMaxWaitDays` → `apid` at deploy (rejects the deploy
  with 400).

### 7. State machine for `workflow_runs.status` and `workflow_steps.status`

**Two state machines, both Pattern B** (the `InvocationState`
precedent at `pkg/state/types.go:1310` + SQL CHECK, no Go
transition map — schedd is the only writer, transitions are gated
by `Store.MarkWorkflow*` methods).

`workflow_runs.status`: 6 states — `pending | running |
awaiting_event | succeeded | failed | dead`.

| From            | Trigger                                       | To              |
|-----------------|-----------------------------------------------|-----------------|
| (insert)        | `apid` POST `/_workflows/<name>/start`        | `pending`       |
| `pending`       | `schedd` claims row, dispatches first step    | `running`       |
| `running`       | step handler returns 2xx, no more steps       | `succeeded`     |
| `running`       | step handler returns 4xx / final step fails   | `failed`        |
| `running`       | step handler returns 5xx after max_attempts   | `dead`          |
| `running`       | step is `wait_for_event`                      | `awaiting_event`|
| `awaiting_event`| event received, dispatch succeeds             | `running`       |
| `awaiting_event`| timeout elapses, `on_timeout` fires (2xx)     | `running`       |
| `awaiting_event`| timeout elapses, `on_timeout` fires (5xx→dead)| `dead`          |
| `awaiting_event`| timeout elapses, no `on_timeout` defined      | `dead`          |
| (any)           | customer `POST /_workflows/<id>/cancel`       | `failed`        |

`workflow_steps.status`: 7 states (the 6 above plus `skipped` for
steps whose `depends_on` resolved to a failure). Same Pattern B
shape.

### 8. Event kinds — explicit decision

The repo has **no central enum** for audit event kinds (the
`events.kind` column has no CHECK constraint; kinds are scattered
across `pkg/events/` and `pkg/audit/`). Two existing patterns:

- `pkg/events/wake.go:35-160` — typed struct constants with a
  `Kind()` method (canonical for the wake lifecycle).
- Freeform strings via `pkg/audit.Auditor.Emit` (e.g.
  `cron.fired` at `pkg/sched/loop.go:1650`).

**Decision for ADR-081**: mirror `wake.go` — a new
`pkg/events/workflow.go` with 9 typed constants implementing a
single `WorkflowEvent` interface. The audit log retains the
existing `kind text not null` column unchanged. New kinds:

- `WorkflowStarted` (run created)
- `WorkflowStepStarted` (step dispatched)
- `WorkflowStepSucceeded` (step returned 2xx)
- `WorkflowStepFailed` (step returned 4xx)
- `WorkflowAwaitingEvent` (wait_for_event step parked)
- `WorkflowEventReceived` (external event landed)
- `WorkflowSucceeded` (run finished, all steps 2xx)
- `WorkflowFailed` (run cancelled or step 4xx with no
  `on_error: continue`)
- `WorkflowDeadLetter` (run exhausted retries or `wait_for_event`
  timeout with no `on_timeout`)

### 9. Retention — mirror `pkg/sched/retention.go`

A new `pkg/sched/workflow_retention.go` with two `SweepOnce`
calls per tick:

- 30 days for `workflow_runs` + `workflow_steps`
  (`WHERE finished_at < now() - interval '30 days'`).
- 90 days for `workflow_events`
  (`WHERE received_at < now() - interval '90 days'`).

Hourly cadence + 1m first-fire delay
(`retentionFirstFireDelay` precedent at
`pkg/sched/watchdog.go:51-61`). Wired into `Loop.Run` via a new
`WithWorkflowRetention(r *WorkflowRetention) *Loop` builder that
mirrors the existing `WithRetention` slot at
`pkg/sched/loop.go:94`.

Why two windows: the `workflow_runs` rows are the customer-facing
audit surface (visible in the dashboard); `workflow_events` rows
are the long-tail event payload archive (used by `wait_for_event`
handlers that look up historical events). 90 days for events
matches the existing 075 retention default.

### 10. Boundary against Temporal / Inngest / Restate

Explicit non-goals (lifted from #669 §"Why this is not a Temporal
competitor"):

- The state lives in **Postgres**, not in a dedicated control
  plane.
- The execution model is **synthetic wakes**, not deterministic
  replay. A failed step is retried by re-running the handler; the
  handler must be idempotent (the customer owns that).
- No versioning, no replay, no signals-from-queries, no
  code-as-workflow. The DSL is declarative (steps + retries +
  events), not imperative.
- The "wait for event" timeout is the only long-park primitive;
  there's no general `sleep` step (a step that needs to sleep for
  1 hour uses a step with `timeout: 1h` and a `wait_for_event`
  step that fires on a `cron`-scheduled self-event).

The ADR explicitly directs customers who outgrow this to Temporal
Cloud / Inngest / Restate.

## Consequences

### Files added (anchors)

- `migrations/00NN_workflows.sql` (goose, append-only) — three
  tables + SQL CHECK constraints + `workflow_due` notify trigger.
- `docs/adr/081-durable-execution-workflows.md` — this ADR.
- `pkg/api/limits.go` — `WorkflowMaxConcurrent`,
  `WorkflowStepMaxTimeout`, `WorkflowMaxWaitDays` fields, plus
  per-plan entries in `planLimits`.
- `pkg/state/workflows.go` — sqlc-generated CRUD on the three
  tables (CreateRun, ClaimRun, MarkStep*, RecordEvent,
  AdvanceRun, CancelRun, ListRuns, GetRun).
- `pkg/events/workflow.go` — 9 typed constants implementing
  `WorkflowEvent` (mirror `pkg/events/wake.go:35-160`).
- `pkg/sched/hostkey.go` — schedd-side X25519 host identity
  loader. **Shared with the future ADR-080 task-queue PR.**
- `pkg/auth/task_token.go` — `Bearer faas_task_<signed>`
  encoder/decoder + `RequireTaskToken` advisory middleware.
  **Shared with the future ADR-080 task-queue PR.**
- `pkg/sched/dispatch_workflows.go` — `runWorkflowsTick`, sibling
  to `runCronTick` (`pkg/sched/loop.go:1515`).
- `pkg/sched/workflow_retention.go` — reaper mirroring
  `pkg/sched/retention.go` (two `SweepOnce` calls, 30d/90d).
- `pkg/sched/loop.go` — registers `runWorkflowsTick` and the
  workflow retention ticker.
- `pkg/gatewaydinternal/workflow_route.go` — `/_workflows/<id>/<step>`
  reserved path, routes to the step's `run` handler, server-side
  replay check (shared Bearer primitive).
- `pkg/api/dto.go` — `WorkflowSpec`, `WorkflowStepSpec`,
  `WorkflowRun` DTOs; add `Workflows []WorkflowSpec` to
  `CreateDeploymentRequest` next to `Sidecars`.
- `pkg/api/errors.go` — `ErrPlanWorkflowsNotAllowed` (402) +
  `ErrPlanWorkflowsQuota` (403), with `CodePlanWorkflowsNotAllowed`
  / `CodePlanWorkflowsQuota` constants.
- `pkg/wire/metrics/workflows.go` — `workflow_runs_total{app,plan,outcome}`,
  `workflow_step_seconds{app,plan,step}` histogram,
  `workflow_awaiting_events{app,plan}` gauge,
  `workflow_dead_letter_total{app,plan,reason}`.
- `sdk/go/client.go` — `StartWorkflow`, `ListWorkflowRuns`,
  `GetWorkflowRun`, `CancelWorkflowRun`, `SendWorkflowEvent`.
- `cmd/faas/cmd/workflows.go` — `faas workflows list / get / start
  / cancel / send-event` (mirrors `faas crons` shape).
- `pkg/e2e/workflows_e2e_test.go` — full lifecycle (happy path,
  retry path, wait_for_event timeout, cancel, replay rejection).
- `docs/STATUS.md` M7.x entry — pins e2e to the wire surface.

### Files modified

- `pkg/api/limits.go` — add the 3 caps + populate per-plan.
- `pkg/sched/loop.go::Loop` struct — `hostIdentity
  *secretbox.Identity` field; `WithWorkflowRetention(...)`
  setter.
- `cmd/schedd/main.go` — load host key on startup alongside PG
  and `GatewaySynth` setup.
- `cmd/gatewayd-internal/main.go` — wire the
  `/_workflows/<id>/<step>` reservation list, the server-side
  replay check, and the per-plan delivery deadline.
- `pkg/db/notify.go` — register `NotifyWorkflowDue =
  "workflow_due"` channel constant.
- `pkg/wire/metrics/metrics.go` — register the 4 new counter /
  histogram families.
- `docs/faas_implementation_spec.md` §4.7.4 — new paragraph on
  workflows.

### Versioning — `definition_snapshot` is the load-bearing field

A new deploy starts new runs against the new definition; in-flight
runs complete against the **old** definition. The mechanism:
each `workflow_runs` row carries a `definition_snapshot` JSONB
copy of the resolved workflow spec at start time. The schedd
reads `definition_snapshot`, never the live deploy. If the
customer changes a step name in the new deploy while a run is
mid-flight, the run's `current_step` pointer still resolves
against the snapshot — no dangling reference.

Documented as a non-goal (no migration of in-flight runs to the
new definition). Customers who need versioning migrate manually
via a cancel + re-start.

### Operational signals

- `workflow_awaiting_events{app,plan}` gauge — operator-facing,
  same shape as `pkg/wire/metrics.instances_gauge`. Alert if
  p99 wait duration > 24h on a Scale customer.
- `workflow_dead_letter_total{app,plan,reason}` — alert at > 0 in
  5 min window (a customer's handler is misbehaving).
- `workflow_step_seconds` p99 — the primary SLO metric for the
  step delivery path, target p99 < 1 s for idle-instance
  delivery, < 5 s for cold-boot delivery.
- `workflow_runs_total{outcome=succeeded}` ratio — the
  customer-facing reliability surface.

### Acceptance (mirrors #669 §"Acceptance")

- [ ] A deploy with a `workflows:` block parses, validates the
      DSL (no circular deps, timeouts within plan), and stores
      the workflow definition on the deployment.
- [ ] `POST /v1/apps/{slug}/workflows/process_order/start` creates
      a `workflow_runs` row in `pending` with `definition_snapshot`.
- [ ] `schedd` advances the run by delivering the first step as a
      synthetic wake within 1 s.
- [ ] A 2xx step response advances the run to the next step.
- [ ] A 5xx step response retries with exponential backoff up to
      `max_attempts`.
- [ ] A `wait_for_event` step parks the run in `awaiting_event`
      and consumes zero RAM (no wake held).
- [ ] A `POST .../events/<name>` payload advances the run.
- [ ] A `wait_for_event` timeout triggers the `on_timeout`
      handler.
- [ ] The dashboard's Workflows page lists runs, drills into
      step history, and supports manual start / cancel.
- [ ] Free plan returns 402 `plan_workflows_not_allowed` on
      start AND on any deploy with a `workflows:` block.
- [ ] `cmd/faas workflows list / get / start / cancel /
      send-event` works end-to-end.
- [ ] `pkg/e2e/workflows_e2e_test.go` exercises the happy path,
      retry path, wait_for_event timeout path, and the replay
      rejection path (Bearer reused after row flips away from
      `running`).
- [ ] DSL validation rejects circular `depends_on` (topological
      sort) at deploy time.
- [ ] State machine — all 10 transitions for `workflow_runs` +
      all 8 transitions for `workflow_steps` covered by unit
      tests on `pkg/state/workflows.go`.

### Risk register

1. **In-flight runs on workflow re-deploy.** Mitigated by
   `definition_snapshot` (§"Consequences — Versioning"). The
   snapshot is taken at run-start time; mid-flight re-deploys
   are ignored by in-flight runs. Documented as a non-goal
   (no migration).
2. **`wait_for_event` long-park at scale.** A Scale customer with
   200 concurrent runs all in `awaiting_event` for 7 days is 200
   idle Postgres rows. Negligible storage cost. Mitigated: the
   `workflow_due` trigger does not fire while
   `status='awaiting_event'`; the row is invisible to the
   dispatch hot path. The retention reaper sweeps
   `succeeded/failed/dead` after 30d regardless.
3. **Wake-storm.** Same Risk #1 from ADR-080 — there is no
   `pkg/sched/rate_limit.go::EnsureInstanceRateLimit` primitive
   today. Each workflow step is a wake; a workflow with 20 ready
   steps on a parked app = 20 cold-boots. **Mitigation**: ADR-081
   inherits whatever ADR-080 picks for wake rate-limiting. Both
   ADRs depend on the same primitive; the coupling is flagged in
   both ADRs' risk registers.
4. **Synthetic-wake Bearer is shared with ADR-080.** PR-A's
   Bearer primitive has two downstream consumers (PR-B: tasks,
   PR-C: workflows). A subtle bug in PR-A affects both.
   **Mitigation**: PR-A includes a Bearer sign/verify round-trip
   e2e + the server-side replay-check test from ADR-080 as a
   precondition for merge.
5. **State machine drift.** Two state machines (instances +
   invocations + workflow_runs) all use the typed-string + SQL
   CHECK pattern but no Go transition map. A column name change
   in one migration can miss another. **Mitigation**: ADR-081
   §"Consequences" documents the convention; future work is a
   shared `pkg/state/machine.go` helper if a third consumer
   lands.
6. **Per-step timeout interaction with the wake reaper.** The
   `WorkflowStepMaxTimeout` is enforced via the `GatewaySynth`
   per-request context deadline; the wake's existing 5 s
   `snapshot_and_park` watchdog fires first if the handler
   genuinely hangs. Both produce a retry via the `running →
   running` transition; document so the implementation does not
   double-enforce.

## Sequencing

ADR-081 ships as one docs-only PR (this ADR), parallel to
ADR-080's docs-only PR. The implementation lands in this PR
chain to keep each PR reviewable in ~10 min per CLAUDE.md:

- **PR-A** (depends on ADR-080 + ADR-081): the shared Bearer
  primitive + schedd hostkey. Files: `pkg/auth/task_token.go`,
  `pkg/sched/hostkey.go`, `cmd/schedd/main.go`, the
  `pkg/api/limits.go::TaskDeliveryDeadlineSec` field (introduced
  by ADR-080). No `public.tasks` or `public.workflow_runs`
  changes. e2e: Bearer sign/verify round-trip + replay check.
- **PR-B** (depends on PR-A): the task queue per ADR-080. Files:
  `public.tasks` migration, `pkg/state/tasks.go`,
  `pkg/sched/dispatch_tasks.go`,
  `pkg/gatewaydinternal/task_route.go`,
  `cmd/apid/handlers/tasks.go`.
- **PR-C** (depends on PR-A): the workflows per ADR-081. Files:
  three-table migration, `pkg/state/workflows.go`,
  `pkg/events/workflow.go`, `pkg/sched/dispatch_workflows.go`,
  `pkg/sched/workflow_retention.go`,
  `cmd/apid/handlers/workflows.go`,
  `pkg/wire/metrics/workflows.go`.

PR-A is the **shared dependency** — both PR-B and PR-C need the
Bearer primitive. This is the architectural answer to "what if
both task queue and workflows want a Bearer": the primitive lands
first, both consumers follow.

## Out of scope (explicit per #669 §"Out of scope")

- Code-as-workflow / deterministic replay — point at Temporal /
  Inngest / Restate.
- Workflow versioning — no migration of in-flight runs to the new
  definition. Mitigated by `definition_snapshot`.
- Cross-app workflows — single-app workflows only.
- Workflow signal / query primitives (Temporal-style) — use the
  `events` table directly via a sidecar if needed.
- Persistent state across wakes — rejected (breaks the niche).
- A richer workflow management UI / runtime deployment path is deferred
  until the stacked workflow runtime change lands; `gregale.yaml` remains
  an additive CLI convenience over the JSON deploy shape.
- Per-app wake rate-limit primitive — Risk #3; either lands as a
  pre-PR (per ADR-080 Risk #1 resolution (a)) or as part of the
  migration PR.
