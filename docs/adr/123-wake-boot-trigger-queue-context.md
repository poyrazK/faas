# ADR-123 · Wake-boot telemetry: trigger + queue-depth + concurrency-at-admit

- **Status:** proposed
- **Date:** 2026-08-21
- **Issue / PR:** wake-timeline completeness (Cloud-Run parity)
- **Decision:** Stamp three additive fields on every `wake.boot_started`
  AND `wake.boot_completed` event row so the platform can answer "why did
  this instance start?" — `trigger` (closed enum of 8 values),
  `queued_count` (engine-side `ledger.Concurrency(app.ID)` at admit time),
  `concurrency_at_admit` (same reading, always populated). The new
  `trigger string` parameter rides through every schedd wake entry point
  (`Engine.Wake` / `Engine.AdmitInstance` / `Engine.AdmitInstanceForDeployment` /
  `Engine.EnsureWake` / `Engine.admitAndDispatch`) and the
  `AdmitInstanceRequest` / `WakeRequest` gRPC messages (field tag 4,
  additive).

## Context

The wake timeline (`wake.*` vocabulary, ADR-064) carries
`method ∈ {"restore","cold_boot"}` and the wall-clock `started_at` /
`completed_at`, but **no information about why the instance started or
what was queueing when it admitted**. Operators investigating a
Hobby-tier latency regression cannot tell apart:

1. a request-driven cold-boot from gatewayd-internal (high-priority
   customer traffic, latency budget §6.3 — should be a snapshot restore),
2. a cron fire-now (operator-initiated, low priority),
3. a 60s cron schedule tick (predictable background work),
4. a per-app floor reconcile (cold-start drift repair),
5. a per-deployment floor sweep (ADR-064 / #555),
6. a target-driven scaleup (CPU / RPS / concurrent_requests axis),
7. a legacy meterd cron wake.

Cloud Run ships a `start_trigger=cold_start|cpu|request|cron` field on
every cold-start log line for exactly this diagnosis. Gregale has no
analog: the customer can see *that* a cold-boot happened, but not
*why*. Hobby / Pro plans that hit SLA complaints get the same opaque
"this instance started at T+0, took 112 ms" line that Scale customers
get for the floor-driven cold-boot the customer didn't ask for.

Issue #517 (correlation + canonical wake timeline) closed in PR #520 /
#524 / PR-C (the typed fan-out seam, the jsonb expression index, and
the customer-facing endpoint) but did not add per-wake trigger
attribution. The pieces for the diagnosis exist in disjoint places —
the wake event itself, the cron-tick `cron.fired` audit row, the
floor-decision counter (`schedd_floor_reconcile_decisions_total`),
the scaleup counter — and an operator must correlate them by
`wake_id` (the only stable join key) to recover the trigger.

The audit (issue #517 closure evidence, ADR-068) flagged this as a
known gap and explicitly deferred it. The Cloud-Run parity primitive
is small enough that the cost of fixing it is dominated by the
review of a single typed-payload extension, not by a new endpoint or
schema.

## Decision

### 1. Closed trigger enum

A single source of truth lives at `pkg/sched/triggers.go`:

```go
const (
    TriggerGateway    = "gateway"          // cmd/gatewayd-internal Edge
    TriggerFloor      = "floor"            // pkg/sched/floor per-app
    TriggerFloorDep   = "floor.deployment" // pkg/sched/floor per-deployment (ADR-064 / #555)
    TriggerScaleup    = "scaleup"          // pkg/sched/scaleup (target-driven)
    TriggerTargets    = "targets"          // pkg/sched/targets (distinct decision axis)
    TriggerCronSched  = "cron.schedule"    // pkg/sched/loop 60s tick
    TriggerCronManual = "cron.manual"      // POST /v1/crons/{id}/run (ADR-090)
    TriggerMeterd     = "meterd"           // legacy Engine.Wake
)
```

The `cron.schedule` / `cron.manual` values are distinct from the
existing `CronDispatchTrigger` enum (`schedule` / `manual` at
`pkg/sched/loop.go:2079, 2083`) — the cron dispatch trigger is an
internal type for the dispatch path; the wake-boot trigger is the
external wire enum. Translation happens at the call site in
`loop.go:2246` (see §4 below).

`targets` and `scaleup` are distinct packages per
`pkg/sched/scaleup/trigger.go:130` (the `concurrent_requests` axis
lives in `pkg/sched/targets`; the scaleup package itself consumes
target metrics but does not own the decision). Both are stamped
separately so an operator can disambiguate a CPU-driven wake from a
concurrent-requests-driven wake.

### 2. `queued_count` and `concurrency_at_admit`

Both are derived from `e.ledger.Concurrency(app.ID)` sampled under the
Phase 2 lock at bootInput construction (`pkg/sched/engine.go:1859`
area). `concurrency_at_admit` is the same reading the `admitGate`
already consults (extended return tuple); `queued_count` is sampled
once at bootInput construction and is identical to the
`concurrency_at_admit` value (the two names reflect "what was the
per-app concurrency when this wake was admitted" — both readers
answer the same question, but the two field names map to the two
Cloud-Run fields in the user's reference line).

The schedd-side `ledger.Concurrency` is the **canonical** source.
The gateway-side `WakeGate.InflightWaiters(appID)` at
`pkg/gateway/gate.go:297-306` reflects "currently-waiting request
count", not "siblings-already-admitted". The two values serve
different purposes; the ledger is the right answer for "what was the
per-app concurrency when this wake admitted". The gateway-side value
is **not** stamped.

### 3. Wire + schema

Additive per ADR-064's compatibility clause. Three new typed fields
on `events.BootStarted` and `events.BootCompleted`
(`pkg/events/wake.go:267-289` and `:296-320`):

```go
Trigger            string // ADR-123 — pkg/sched/triggers.go closed enum
QueuedCount        int    // ADR-123 — ledger.Concurrency at admit
ConcurrencyAtAdmit int    // ADR-123 — same reading; 0 is cold start
```

`Payload()` adds three keys (`queued_count`, `concurrency_at_admit`,
and conditional `trigger`) — mirror the optional-field pattern from
`ParkStarted.Payload()` at `pkg/events/wake.go:439-441` (the
`DeploymentID` opt-in). `trigger` is conditional (absent on the
Phase-1 fast-path return where an existing RUNNING instance was
reused — `pkg/sched/engine.go:1119`); `queued_count` and
`concurrency_at_admit` are unconditional (always present, possibly
zero — the cold start case).

`AdmitInstanceRequest` and `WakeRequest` in
`api/proto/onebox/faas/schedd/v1/schedd.proto` get a new
`string trigger = 4` field. Tag 4 verified unused in both messages;
additive per the wire-additive rule (the generated `.pb.go` files are
regen'd via `make proto` and committed per the existing repo
convention; CI gate `make proto-check`).

### 4. Engine plumbing

Five entry-point functions on `Engine` accept a new `trigger string`
parameter:

| Function | File:line | Notes |
|---|---|---|
| `Engine.Wake` | `engine.go:~1032` | Legacy fast path used by meterd; stamp as `meterd` defensively |
| `Engine.AdmitInstance` | `engine.go:1257` | PGBackend hot path |
| `Engine.AdmitInstanceForDeployment` | mirror AdmitInstance | Per-deployment floor sweep |
| `Engine.EnsureWake` | `engine.go:1161` | Single-flight ADR-098; leader's trigger wins on the coalesced wake row |
| `Engine.admitAndDispatch` | `engine.go:1424` | Internal funnel |

The `trigger` argument is captured in the `bootInput` struct
(`engine.go:2289-2326`, extended with three new fields) under the
Phase 2 lock and consumed by both `BootStarted` (Phase 3 entry,
`engine.go:1987-1997`) and `BootCompleted` (Phase 4 commit,
`engine.go:2269-2281`) emits. The values are immutable across the
unlocked Phase 3 window so both rows carry the same snapshot.

`admitGate` (`engine.go:~5585`) returns one extra `int` — the
`ledger.Concurrency(app.ID)` reading the gate already consults. A
single read under the lock, captured into bootInput.

The seven caller sites stamp one of the eight closed-enum values:

| Caller | File:line | trigger arg |
|---|---|---|
| floor per-deployment | `floor/trigger.go:525` (`AdmitInstanceForDeployment`) | `TriggerFloorDep` |
| floor per-app | `floor/trigger.go:640` (`EnsureWake`) | `TriggerFloor` |
| scaleup | `scaleup/trigger.go:406` (`EnsureWake`) | `TriggerScaleup` |
| targets | `pkg/sched/targets/trigger.go` (`EnsureWake`) | `TriggerTargets` |
| cron schedule tick | `loop.go:2130` → `loop.go:2246` (`EnsureWake`) | `TriggerCronSched` (translated from `CronDispatchTrigger=schedule`) |
| cron fire-now | `loop.go:2392` (`EnsureWake`) | `TriggerCronManual` (translated from `CronDispatchTrigger=manual`) |
| gatewayd-internal | `pkg/gateway/handler.go:4826, 5970, 5997` (via `Backend.Admit` → `sched.AdmitInstance`) | `TriggerGateway` |
| legacy `Engine.Wake` | no current caller; defensive | `TriggerMeterd` |

### 5. Surfacing

Three surfaces consume the new fields without new endpoints:

1. **CLI**: `gregale wake-timeline <slug> <wake-id>` adds the trigger
   + queued + concurrency to each per-event line and a
   `triggers: <enum>=N ...` summary header (stable sort). Renderer
   extension at `cmd/gregale/commands_wake_timeline.go:140-145`.

2. **apid**: `GET /v1/apps/{slug}/wakes/{wake_id}/timeline` already
   returns the jsonb `data` map verbatim
   (`cmd/apid/handlers_wake_timeline.go:197-213`). No code change —
   the new keys surface in the existing `--json` path automatically.

3. **Dashboard**: the existing `Recent wakes` table on
   `app_detail.html:186-212` extends with three new columns
   (Trigger / Queued / Concurrency) backed by the new batched
   `Store.LookupBootStartedForWakes` call from
   `cmd/apid/handlers_dashboard.go` (one SQL round-trip, gated by
   `events_wake_id_idx` from `migrations/00114_events_wake_id_idx.sql`);
   no new index. The dedicated `/dashboard/apps/{slug}/wake-timeline`
   per-wake narrative view (a `pkg/dashboard/views/wake_timeline.go`
   helper + matching template) lands in the **PR-A follow-on** —
   the §PR-A follow-on subsection below describes the dedicated view
   + the two additional wake-boot fields it surfaces.

Pre-ADR-123 rows in the dashboard render `—` (the existing convention
from `app_detail.html` for absent values).

## Why now

Issue #517 closed the wake timeline. This closes the "but I can't
tell *why* it woke" sub-gap without a new endpoint. Cloud-Run
parity for cold-start telemetry is the motivating customer-visible
primitive. Hobby-tier latency regression investigations split
cleanly by `trigger` (cron vs scaleup vs gateway). The fields live
on existing rows (`events.data->>'trigger'` etc.) so existing
timeline / dashboard queries get the data with no migration.

## Consequences

- **Positive**: operator's "why did this wake?" pane answers in one
  row. Hobby-tier latency regression investigation splits cleanly
  by `trigger`. Cloud-Run parity for cold-start telemetry.
- **Positive**: supersedes the ADR-072 / issue #555 "floor dual-emit"
  pattern — operators no longer need to join `floor.wake` audit
  events to learn why an instance woke; the trigger is on the boot
  row itself. Future PR can retire the dual-emit safely.
- **Negative**: ~22 bytes per boot row (trigger string + two ints).
  At ~13 rows / cold-wake, this is negligible against the existing
  payload footprint.
- **Compatibility**: see ADR-064 §Compatibility — additive only. The
  typed struct change lights up the compile-time interface check at
  `pkg/events/wake_test.go:13-37`; every literal updates. No
  call-site outside the schedd engine and vmmd observation is affected.
  Schedd remains the only `BootStarted` source; vmmd emits the distinct
  `BootObserved` type so customer counts cannot interpret the RPC-boundary
  corroboration as a second boot decision. `BootObserved` carries only
  correlation, node, method, and observation-time fields; it never repeats
  scheduler-owned trigger, queue, concurrency, capacity, or tier values.
- **Migration**: jsonb additive — no schema migration strictly
  required. The optional analytics expression index on
  `data->>'trigger'` is **deferred** (no dashboard panel needs
  cross-app aggregation yet; the existing `events_wake_id_idx`
  covers the per-wake JOIN).
- **Tests**: shape tests (`pkg/events/wake_test.go`), per-trigger
  table-driven engine test (`pkg/sched/engine_test.go`), gateway
  threading test (`pkg/gateway/pgbackend_test.go`), proto
  round-trip test (`api/proto/onebox/faas/schedd/v1/`).
- **Spec**: §17 G17 row added (closes the gap). Cross-references
  in §6 lifecycle and §12 observability.

## Rejected alternatives

1. **Separate `trigger_events` table.** Rejected. Doubles write
   volume, breaks the "one events row per phase" contract
   ADR-064 closed, and operators expect to find trigger on the
   same row as the boot event.
2. **Computed-on-read view.** Rejected. Adds a per-query JOIN
   that defeats the customer-facing timeline endpoint's 1-page
   latency budget. The field is *emitted*, not *derived*.
3. **Post-hoc join against `cron.fired` / `floor.wake` /
   `instances.warmed_min_instances`.** Rejected. Already attempted
   as the dual-emit (ADR-072 §Decision 6); adds customer-facing
   coupling and a fragile dependency on audit-event kind names.
   The field-on-event pattern is the canonical Cloud-Run / OTel
   move.
4. **Use `WakeGate.InflightWaiters` instead of `ledger.Concurrency`
   for `queued_count`.** Rejected. The gateway-side count
   reflects "currently-waiting request count", not "siblings
   admitted". The two values answer different questions; the
   ledger is the right primitive for the user's reference line
   ("8 requests were waiting" means 8 admitted-or-admitting,
   not 8 in-flight-in-the-gateway-queue).
5. **Defer indefinitely.** Rejected. Hobby-tier SLA complaints
   are the customer-facing cost; the engineering cost of
   stamping three fields is bounded by the typed-struct
   extension and the engine plumbing in §4. The audit
   (ADR-068) flagged this as the next deferred item; picking it
   up now keeps the wake-timeline story closed rather than
   perpetually in-progress.

## Neighbors

- **ADR-064** (wake timeline vocabulary — compatibility clause for
  additive payload changes; partial jsonb index pattern).
- **ADR-068** (issue #517 closure evidence — explicitly deferred
  trigger attribution; this ADR closes it).
- **ADR-072** (floor dual-emit pattern — explicitly superseded by
  this ADR's field-on-boot-row approach).
- **ADR-090** (cron dispatch trigger — CronDispatchTrigger enum
  source; this ADR's `cron.schedule` / `cron.manual` values
  translate from it).
- **ADR-097** (wake-phase histogram — metric counterpart; the
  per-wake trigger field is the typed event counterpart).
- **ADR-098** (single-flight EnsureWake — engine plumbing; the
  `trigger` parameter flows through `EnsureWake`).
- **ADR-099** (wake rate-limit — adjacent capacity decision).

## Critical files

- `pkg/events/wake.go` — payload schema extensions
- `pkg/sched/engine.go` — `bootInput`, `admitAndDispatch`,
  emission sites, `Engine.Wake` / `AdmitInstance` / `EnsureWake`
- `pkg/sched/triggers.go` — NEW, closed-enum constants
- `api/proto/onebox/faas/schedd/v1/schedd.proto` — wire field
- `pkg/gateway/handler.go:468` + `pkg/gateway/pgbackend.go:795` —
  `Backend.Admit` signature
- `cmd/gregale/commands_wake_timeline.go:140-145` — CLI renderer
- `pkg/dashboard/views/wake_timeline.go` — NEW, dashboard table renderer
- `pkg/dashboard/dashboard.go:307-321, 591` — query + `RecentInstanceItem`
- `pkg/dashboard/templates/app_detail.html:186-212` — recent-wakes columns
- `pkg/dashboard/templates/app_wake_timeline.html` — NEW, per-app wake-narrative page
- `cmd/apid/handlers_dashboard.go` — `parseWakeTimelinePath` +
  `renderAppWakeTimeline` + dashboardHandler route branch
- `pkg/state/pgstore.go::LookupBootStartedForWakes` — SQL widened with
  `at_capacity` (COALESCE) + `ready_in_ms` (LEFT JOIN LATERAL against
  `wake.boot_completed`)
- `pkg/state/types.go::WakeBootMeta` — extended with `AtCapacity` + `ReadyInMS`
- `pkg/sched/engine.go` — `admitGate` return tuple widens with
  `atCapacity bool`; stamped on `BootStarted` via `bootInput.atCapacity`
- `pkg/events/wake.go` — `BootStarted.AtCapacity` + `Payload()` addition

## PR-A follow-on (closed 2026-08-21)

This ADR originally deferred three items to a follow-on PR. They
shipped in **PR-A**, branch `feat-adr-123-pr-a-wake-narrative`
(head `b6f5fc5f4`), 4 atomic commits. Net change: +482 / -18 across
7 files (dashboard / state / sched packages).

### A1. Dedicated `/dashboard/apps/{slug}/wake-timeline` page

A new handler `renderAppWakeTimeline` in
`cmd/apid/handlers_dashboard.go` (paired with `parseWakeTimelinePath`
that mirrors `parseDomainDoctorPath`) renders a per-app page with:

- 24h summary card: total wakes + trigger histogram (stable sorted
  via `views.RenderTriggerHistogram`) + at-capacity count + at-cap %.
- Recent wakes table (up to 50 rows) pre-rendered at the handler
  edge via `views.RenderWakeTimelineTable` (FuncMap-free, stages
  pattern). All values escaped via `template.HTMLEscapeString`
  before the chassis cast; the G203 gosec annotation mirrors the
  precedent at `pkg/dashboard/views/render.go:274/311`.

### A2. Two new wake-boot fields

Two additional fields close the Cloud-Run reference line's
"Existing instances: 2/2 at concurrency limit" + "Ready in: 112 ms"
tails:

- `at_capacity` (bool) — true when `admitGate`'s `wakeAdmit` branch
  observed `concurrency+1 >= maxConc` (the per-app plan ceiling).
  Always stamped on PR-A fleet rows; pre-PR-A rows default to `false`
  via SQL `COALESCE((data->>'at_capacity')::bool, false)`.
- `ready_in_ms` (int) — `EXTRACT(EPOCH FROM (boot_completed.at -
  boot_started_at)) * 1000` via `LEFT JOIN LATERAL` against the
  matching `wake.boot_completed` row. PR-A review-cluster fix: the
  initial `EXTRACT(MILLISECONDS FROM …)` implementation was
  silently wrong for any delta ≥ 60 s because PostgreSQL intervals
  are stored as months/days/seconds and `EXTRACT(MILLISECONDS …)`
  returns ONLY the seconds-field milliseconds (verified via psql:
  an interval of `65.5 seconds` extracted to 5500 ms, not 65500).
  `EXTRACT(EPOCH …)` returns total elapsed seconds and `* 1000`
  yields the wall-clock millisecond delta — accurate for any
  duration. Zero when no boot_completed exists (still booting or
  rejected); the template renders em-dash per the existing
  absent-value convention.
- `at_capacity_present` (bool) — distinguishes "jsonb key absent"
  (pre-PR-A fleet row that lacks the at_capacity key entirely)
  from "jsonb key present and explicitly false" (PR-A row that
  was admitted below the cap). Source: pgstore's
  `data ? 'at_capacity'` jsonb contains operator (NULL when the
  key is absent). The dashboard's em-dash-on-absent convention
  depends on this — a pre-PR-A fleet row renders "—" (we don't
  know) instead of "No" (we know it wasn't at the cap).

Both flow through the existing wire (`events.data` jsonb,
additive) and surface in the dashboard's "Recent wakes" table
plus the new wake-timeline view.

### A3. New tests (3)

- `pkg/sched/floor/trigger_lockstep_test.go` +
  `pkg/sched/scaleup/trigger_lockstep_test.go` +
  `pkg/sched/targets/trigger_lockstep_test.go` — external-test
  packages (`package floor_test` etc.) with `export_test.go`
  surfaces that pin `pkg/sched/floor.WakeBootTriggerFloor` /
  `WakeBootTriggerFloorDep` etc. against the canonical
  `sched.TriggerFloor` enum. Closes the lockstep audit finding
  flagged in PR #1015 review #4. The cross-package import
  (`floor_test → floor + pkg/sched`) is test-time only; no
  production cycle because production `pkg/sched → floor`
  via `loop.go` doesn't import `_test`.
- `pkg/state/lookup_boot_started_test.go` — `pgtest.Open()`-gated
  round-trip test that inserts a canonical + mirror row for one
  wake_id and asserts `LookupBootStartedForWakes` returns the
  canonical one (`trigger='gateway'`, not `'mirror'`) via the
  `DISTINCT ON (bs.wake_id) … ORDER BY bs.wake_id` canonical-row
  preference. Closes PR #1015 review #3.
- `pkg/events/wake_test.go::TestBootStarted_AtCapacity` — table-driven
  shape test asserting `at_capacity` is always present (unconditional,
  not gated by `if e.AtCapacity`) and typed as `bool` not `string`.

### A4. Migration slot

**No new migration.** The `at_capacity` + `ready_in_ms` fields
land on the existing jsonb payload (additive, ADR-064 compat
clause). The dashboard's `LEFT JOIN LATERAL` against
`wake.boot_completed` is bounded by the existing
`events_wake_id_idx` partial index from migration 00114. PR-A
deliberately follows the "no migration strictly required"
posture the original ADR settled on.

### A5. Risk

- The `atCapacity bool` widening on `admitGate`'s return tuple
  touches 6 call sites in `pkg/sched/engine_test.go` and 1 in
  `engine.go`. Compile-time force.
- The `ready_in_ms` SQL adds a LATERAL JOIN — sub-millisecond
  per query at the dashboard's per-page batch (≤50 wake_ids);
  no observability delta.
- Pre-PR-A fleet rows render `false` / `0` / em-dash on the new
  fields via the COALESCE + view-layer conventions; no historical
  data is lost or hidden.

## Audit follow-on items (closed by PR-A)

The three deferred items below were originally captured in this
ADR's "open follow-on" subsection. PR-A resolves all three:

1. ~~Dedicated `/dashboard/apps/{slug}/wake-timeline` page~~ → A1.
2. ~~Trigger-constants lockstep test~~ → A3.
3. ~~pgstore round-trip test for `LookupBootStartedForWakes`~~ → A3.

Two additional items (AtCapacity + ReadyInMS) were added to PR-A's
scope after the PR-A plan was approved; both ship in A2.
- `docs/faas_implementation_spec.md` — §17 G17 row + §6/§12 cross-refs
