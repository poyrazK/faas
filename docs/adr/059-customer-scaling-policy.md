# ADR-059 — Customer-configurable scaling policy (issue #462)

- **Status:** proposed
- **Date:** 2026-08-01
- **Amended:** 2026-09-22 (bounded warm-saturation admission queue)
- **Issue:** #462
- **Decision:** ship a per-app `ScalingPolicy` (DTO + persistence +
  inflight signal + engine cooldown + worker carve-out + 503 wire
  shape) across 4 sequenced PRs (PR-A persistence, PR-B inflight
  signal, PR-C engine cooldown + target tracking, PR-D worker
  carve-out + `CodeWaitForWarm`). Closes issue #462.

## Why

The metric surface was already on main before this work
(ADR-037 `schedd_scale_up_decisions_total{app, outcome}`,
ADR-038 `schedd_scale_down_decisions_total{app, outcome}`, the
47,600 MB multi-box ceiling, the wake-gate `AdmitInstance` seam).
What's missing is the **customer-facing policy layer** between
those primitives and the customer. Today a customer can deploy an
app on a plan with a fixed `MaxConcurrency` and a fixed idle
timeout, but they cannot configure per-app knobs like
`min_instances`, `max_instances`, `target.metric`, or
`scale_out_cooldown_s`. The lack of a customer-facing surface
forces every workload onto the plan's max — Hobby customers can't
scale beyond 2 concurrent instances, even if their disk + memory
budget supports 5. Pro / Scale customers over-provision their
budget to ride out bursty traffic.

Per the financial model spreadsheet, the Hobby tier (5 / 2 /
256 / 50) is the break-even ceiling for the platform's smallest
paid customer. The Free-to-Hobby break-even window is 1 GB-h vs
50 GB-h (PR-A's Hobby tier-up). The customer-facing scaling policy
lets a Hobby customer configure `max_instances=5` (the new
plan-bounded ceiling) without forced tier-up — the model's
"predictable bills" invariant (CLAUDE.md) is preserved because the
50 GB-h monthly ceiling remains plan-bounded.

The metric surface, the per-instance max-inflight signal, and the
persistence layer are all on main; PR-A persisted the schema,
PR-B wired the inflight signal, PR-C closed the engine + trigger
gap, and PR-D ships the worker carve-out + the financial ADR.
This is the closing ADR.

## Decision

**1. DTO + persistence (PR-A; PR #493 merged).**
`state.ScalingPolicy` carries `MinInstances`, `MaxInstances`,
`Target *ScalingTarget`, `ScaleOutCooldownS`, `ScaleInCooldownS`.
Custom `MarshalJSON`/`UnmarshalJSON` keeps the wire shape
contractor-friendly (puts `target` under a single object).
`pkg/api/dto.go` mirrors the struct plus a `SetScalingPolicy`
boolean for partial updates. `pkg/api/limits.go` Hobby tier-up
flips the Free→Hobby break-even from 1 GB-h to 50 GB-h.
Apps row carries `last_scale_out_at` * `last_scale_in_at`
(`migrations/00082`).

**2. Inflight signal (PR-B; PR #501 merged).**
`pkg/fcvm/activity` Begin/End cache supplements the vmmd wire
with an in-memory inflight counter; `Reader.MaxInflightForApp`
is the consumer. Without the inflight signal, the customer-facing
`target.metric=concurrent_requests` knob has no source — the
PR-D wake-gate consult cannot decide admit vs. no-signal.

**3. Engine cooldown + outcomes (PR-C; PR #507 merged).**
`Engine.admitGate` consults the stamp + the per-app conc before
`Admit`. Four outcomes: `wakeAdmit`, `wakeRejectAtCap`,
`wakeCooldownHeld`, `wakeMinFloorAlready`. Each emits to
`scal e_up_decisions_total{outcome}`. `pkg/sched/targets.Trigger`
is the customer-facing concurrent_requests trigger (mirror of
`pkg/sched/scaleup.Trigger` for RPS / CPU). The
`Concurrency > 0` discriminator is load-bearing: a cold start
with concurrency == 0 bypasses cooldown so a request-driven
wake is never deferred on a freshly-stamped column.

**4. Worker carve-out (PR-D).**
`Engine.admitAndDispatch` adds a `WorkloadClassWorker`
first-check BEFORE `admitGate`, mirroring `pkg/sched/reaper.go:170`
(workers are reaper-exempt). The carve-out lifts to
`WakeResult{AtCapacity: true}, nil` — the existing typed-capacity
path. No new `wakeOutcome`, no new metric row. The targets
trigger's `AtCapacity` branch re-observes `RejectAtCap`
(passed-through carrier). The check fires BEFORE the gate
so the cooldown / reject / min-floor branches are unreachable
for worker-class apps.

**5. `CodeWaitForWarm` 503 + `Retry-After` (PR-D).**
The `wakeCooldownHeld` branch now surfaces as a 503 + `Retry-After`
with the cooldown remaining seconds. Distinct from
`CodePlanLimitConcur` (429, the customer's plan is fine; their
`ScaleOutCooldownS` is holding the wake). The constructor
`api.ErrWaitForWarm(cooldownS int, l Limits, observed int)`
bounds `cooldownS` at 1 (RFC 7231 §7.1.3 forbids 0/negative).
`StatusForCode(CodeWaitForWarm) == 503` is the inverse-table
mapping that `pkg/grpcerr.FromStatus` reads to lift the gRPC
code back to the right HTTP status.

**6. `pkg/grpcerr.codeToGRPC` extension (this PR; v1 deferred).**
The gRPC mapping itself is unchanged (cooldown_held still
collapses to `ResourceExhausted` like `CodePlanLimitConcur`).
The HTTP-side lift via `StatusForCode` is the load-bearing
addition. PR-D ships the wire shape; an ADR follow-up can
add a dedicated gRPC code class if a customer needs to
distinguish 429 from 503 on the gRPC side.

**7. Test surface (PR-D).**
- `TestErrWaitForWarm` + `TestErrWaitForWarm_BoundsAtOne` +
  `TestErrWaitForWarm_FlushedOnWire` + `TestStatusForCode_WaitForWarm`
  in `pkg/api/errors_test.go` pin the wire shape.
- `TestAdmitAndDispatch_WorkerClassExempt` (worker-class
  bypass + non-worker regression pin) +
  `TestAdmitAndDispatch_CooldownSwitchedToWaitForWarm` (503 +
  Retry-After math) + `TestAdmitAndDispatch_MinFloorAlready_StaysPlanLimitConcur`
  (PR-D exclusion pin) in `pkg/sched/engine_test.go`.
- `TestProperty_EngineWake_RespectsCooldown` extended in
  `pkg/sched/invariants_property_test.go` to assert
  `CodeWaitForWarm` + `Retry-After` on the gate-denied wakes.

**8. Out of scope (explicit deferrals).**
- **No new `wakeOutcome` value.** PR-D's worker carve-out is a
  first-check before `admitGate`. The 4-outcome enum stays. This
  avoids a new closed-set Prometheus label on `pkg/wire/metrics.go`
  and keeps the metric taxonomy stable.
- **No `RetryAfterSeconds` body field.** The existing `WithHeader`
  pattern is the v1 contract. A customer-facing JSON field is a
  follow-up ADR if SDKs need it.
- **No SDK surface widening.** `CreateApp`/`UpdateApp` already
  accept `ScalingPolicy` (PR-A). The 503 wire is consumed by the
  gateway's wake-error path; no SDK method changes.
- **No dashboard "warm-up in progress" pill.** The
  `{outcome=cooldown_held}` counter exists from PR-C; the dashboard
  is a follow-up story.
- **No admin-side override for `ScaleOutCooldownS`.** Customer-facing
  field only; admin tier-up stays on `MinInstancesAllowed` etc.
- **No new migration.** PR-A's `apps.last_scale_out_at` is on main
  (slot 82). No schema changes; PR-D renumbers nothing.

**9. Bounded warm-saturation admission queue (2026-09-22 amendment).**
`ScalingPolicy` gains `concurrency_overflow`, `max_queue_depth`, and
`max_queue_wait_ms`. `drop` retains immediate rejection; `queue` admits
requests through a per-app FIFO that is separate from the cold-wake queue.
Both depth and time are bounded by plan defaults in `pkg/api/limits.go`:
Free 8 / 1 s, Hobby 32 / 2 s, Pro 128 / 2 s, and Scale 512 / 2 s.
Per-app overrides may lower or raise the depth up to 8× the plan default and
may set a wait up to 30 s; zero selects the plan default.

Queue overflow is HTTP 429 `concurrency_queue_full`; wait expiry is HTTP 503
`concurrency_queue_timeout`. Both include `Retry-After`. Successfully admitted
requests expose `X-Gregale-Queue-Wait-Ms` and `Server-Timing: gregale_queue`,
and operators receive bounded-cardinality queue depth and wait histograms.
The effective values are returned by the API and shown by the CLI so users do
not need to reconstruct plan defaults. This amendment does not change instance
ceilings, cold-wake admission, billing, or scheduler placement.

**Fleet semantics (2026-09-22 amendment 2).** `max_queue_depth` is one
fleet-wide per-app admission budget, not a per-`gatewayd-internal` multiplier.
Gateways acquire short-lived Postgres permits only after all warm targets are
saturated. Request bodies remain in the accepting process; the database never
becomes a request broker. Each gateway preserves FIFO among requests it
accepted, while ordering across gateways is intentionally unspecified because
the public edge does not pin an app to one internal replica. A permit is
released when its local request leaves the queue and expires after the request
wait budget plus a five-second crash-recovery grace period.

Shared admission fails closed with a retryable 503 when Postgres cannot be
consulted. Falling back to a local counter would violate the customer-visible
fleet cap exactly during an infrastructure fault. Expired permits are reaped
under an app-scoped transaction advisory lock on the next admission attempt;
unrelated apps never share a logical lock (a hash collision can only serialize
their attempts). Queue-depth metrics remain replica-local so Prometheus may
aggregate them without every replica publishing the same global gauge.

## Failure modes

| Scenario | Behaviour |
|---|---|
| Cold-start with `concurrency == 0` and stamped cooldown | Bypass (cold-start discriminator in `isOnScaleOutCooldown`). Wake proceeds. |
| Worker-class wake on a Free plan | `AtCapacity=true` (no metric row, no wake, no error). |
| Customer PATCHes `target.metric=concurrent_requests` on a worker-class app | 422 `CodeScalingTargetIncompatibleWithWorkloadClass` (PR-A's apid gate). |
| Cooldown remaining ≤ 0 in the dust after the halt | `Retry-After: 1` (the helper bounds at 1; RFC 7231 §7.1.3). |
| `LastScaleOutAt == nil` race during a concurrent wake | Cold-start bypass on the consult side (NULL → no cooldown). Safe direction. |
| `cooldownSRemaining` called twice between two cooldowns | The second call sees new `LastScaleOutAt` and recomputes (no caching). |
| Clock skew between schedd and Postgres (chrony on the reference node) | Bounded: helper bounds at 1; a skew-induced 0 never emits `Retry-After: 0`. |
| `wakeMinFloorAlready` branch fires on a stamp-cooldown gap | Stays on `CodePlanLimitConcur` (429). Distinct from cooldown_held. |
| `Concurrency > 0` discriminator removed by accident | Every request-driven wake hits cooldown; customer's "scale on demand" use case breaks. Caught by `TestAdmitGate_Outcomes/cold_start_bypass_cooldown`. |
| `codeToGRPC` table drops CodeWaitForWarm | Inverse lift emits 500 (default). Caught by `TestStatusForCode_WaitForWarm`. |

## Security

- **No widening of §11.** No new cgroup / uid / netns surface.
  The worker carve-out is a typed-capacity lift; no VM lifecycle
  change. The 503 wire shape is purely an HTTP status + `Retry-After`
  header.
- **Cooldown remains a customer-controlled knob.** PR-D adds
  the wire shape; the customer still sets `scale_out_cooldown_s`
  via the existing PATCH path. No admin override, no surface
  widening.
- **The 503 wire is not a backdoor.** The customer's request is
  not authorized; the wake is held by their own cooldown. The
  gateway's `writeWakeError` flushes `Retry-After` from the
  existing `extraHeaders` chain; no new auth path.
- **Worker-class apps cannot opt into PR-C's target tracking.**
  The apid gate already rejects `target.metric=concurrent_requests`
  for worker-class apps (PR-A). The engine-side carve-out is
  defense in depth: a customer who bypasses the apid gate
  still sees `AtCapacity=true` on the wake path.

## Consequences

- **`pkg/api/errors.go`** gains `CodeWaitForWarm` constant,
  `StatusForCode(CodeWaitForWarm) == 503` mapping, and
  `ErrWaitForWarm(cooldownS int, l Limits, observed int)` constructor.
- **`pkg/sched/engine.go`** gains `cooldownSRemaining(app, now)` helper
  (top-level function — clock-injected for testability), the
  worker-class first-check in `admitAndDispatch`, and the switch
  split that routes `wakeCooldownHeld` → `CodeWaitForWarm` and
  `wakeMinFloorAlready` → `CodePlanLimitConcur` (429, no Retry-After).
- **`pkg/sched/engine_test.go`** + **`pkg/sched/invariants_property_test.go`**
  gain the new tests (see §Decision 7).
- **`pkg/api/errors_test.go`** gains the four wire-shape pin tests.
- **No new SDK method.** `CreateApp` / `UpdateApp` already accept
  `ScalingPolicy` (PR-A). The 503 wire is consumed by the gateway's
  wake-error path; no SDK method changes.
- **No `pkg/api/limits.go` change.** Cooldown values are bounded
  by the DTO's `ErrInvalidCooldown` rules (PR-A). No quota
  relax/widen is needed.
- **No migration.** Column already on main
  (`migrations/00082_scaling_policy.sql`).
- **No SDK regression.** `Error.Wake` semantics unchanged: a
  cooldown_held wake now surfaces as `CodeWaitForWarm` (503 +
  Retry-After) instead of `CodePlanLimitConcur` (429). The
  semantic is a strict UX improvement (smart backoff).
- **No dashboard change.** The `cooldown_held` counter exists
  from PR-C; the dashboard "warm-up in progress" pill is a
  follow-up story.

## Rejected alternatives

- **New `wakeWorkerClass` outcome.** Adds a closed-set Prometheus
  label for a chart-bar that conveys nothing (worker-class always
  sees no_signal). The first-check + AtCapacity lift keeps the
  metric taxonomy stable and the carrier semantic unchanged.
- **`RetryAfterSeconds` JSON body field.** Not in the v1 contract;
  the `WithHeader` pattern is the existing surface. A
  customer-facing JSON field is a follow-up ADR if SDKs need it.
- **Async fire-and-forget wake.** Wake must always work (ADR-005).
  The retry semantics belong in the gateway, not the customer.
- **Drop the `Concurrency > 0` discriminator.** Cold-start bypass
  is load-bearing. Removing it would let every request-driven
  wake hit cooldown and defeat the "scale on demand" use case.
- **Stuck on `CodePlanLimitConcur` (429) for cooldown_held.** The
  customer's request is not a "plan limit" violation; their plan
  is fine, their `ScaleOutCooldownS` is holding the wake. Mixing
  the two semantics on the same code would force the dashboard
  to do string-sniffing.
- **Wire `CodeWaitForWarm` as 429.** Some 429s are retryable
  (Retry-After is RFC 7231 mandatory for 503 + 429 + 3xx). The
  503 status is the right convention for "service is here but
  temporarily busy" — and the v1 contract mirrors the existing
  `CodeCapacity` (503, no Retry-After) and `CodeBuildXXX` (503,
  no Retry-After) precedent.
- **Inject `Retry-After` on the gRPC side via `grpc-status-details-bin`.**
  The gateway-egress surface is HTTP; the gRPC-side `Retry-After` is
  lossy (gRPC has no equivalent). The HTTP-side `WithHeader` is
  the canonical surface.

## Downstream

- **Issue #462 closes.** PR-D merges the wire shape + the worker
  carve-out + the ADR. The customer-facing surface is complete.
- **PR-D follow-ups (out of scope here):**
  - ADR-060 (proposed): v2 contract for `RetryAfterSeconds` body
    field if SDKs need the JSON form.
  - ADR-061 (proposed): dashboard "warm-up in progress" pill
    driven by `{outcome=cooldown_held}` counter.
  - ADR-062 (proposed): `pkg/grpcerr.codeToGRPC` dedicated branch
    for `CodeWaitForWarm` (currently shares `ResourceExhausted`
    with `CodePlanLimitConcur`).
  - Future: per-app `WorkloadClassWorker` `MinInstances` API
    surface (workers are invariant-§6.2-1 *combined* with plan cap,
    but currently the wake path bypasses both; this is fine for the
    v1 contract but a customer-facing API gap).
- **Financial model impact.** PR-A's Hobby tier-up (5 / 2 / 256 / 50)
  + PR-D's `MaxInstances` field lets Hobby customers scale up to 5
  concurrent instances without forced tier-up. The 50 GB-h monthly
  ceiling remains plan-bounded; the model's "predictable bills"
  invariant is preserved.

## Reused on main (no redesign)

- `CodePlanLimitConcur`, `CodeAppConcurReached`, `CodeCapacity` —
  `pkg/api/errors.go:140, 426, 156`. PR-D keeps these 429/503
  surfaces unchanged and adds `CodeWaitForWarm` adjacent to
  `CodeCapacity` (503 + `Retry-After`).
- `*api.Problem.extraHeaders` + `WithHeader` chain —
  `pkg/api/errors.go:64-69, 117-123, 81-90`. Same pattern as the
  existing `Retry-After: 5` on `CodeCapacity` (engine.go:897-900,
  1351-1354).
- `WakeResult.AtCapacity bool` — `pkg/sched/engine.go:438`. The
  worker-class gate uses this carrier to lift to a typed
  capacity result.
- `Engine.admitGate` + `wakeOutcome` enum — `pkg/sched/engine.go:2098-2170`.
  The 4 outcomes (`wakeAdmit`, `wakeRejectAtCap`, `wakeCooldownHeld`,
  `wakeMinFloorAlready`) stay; PR-D adds the worker-class
  first-check BEFORE `admitGate` so no metric row is emitted for
  workers.
- `pkg/sched/reaper.go:170, 256` — the `if in.WorkloadClass ==
  state.WorkloadClassWorker { continue }` pattern is the model
  for the wake-path gate.
- `pkg/grpcerr/grpcerr.go::codeToGRPC` (line 34-66) — the single
  gRPC switch point; PR-D adds the new code's branch.
- `pkg/api/errors.go::StatusForCode` (line 496-608) — the single
  HTTP inverse table; PR-D adds the new code's branch.
- `pkg/gateway/handler.go::writeWakeError` (line 1415-1429) — the
  gateway flushes `extraHeaders` from any `*api.Problem` via
  `errors.As(err, &prob)`; no PR-D extension needed.
- `pkg/api/errors_test.go::TestStatusForCode_AlertRules`
  (line 553-569) — the closed-set pin for the new code's
  inverse-status mapping.
- `pkg/sched/reaper_test.go` cooldown tables (line 595-665) — the
  test pattern for the wake-gate tests.
