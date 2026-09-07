# ADR-060 — Per-app GB-h floor for `min_instances > 0` (issue #515)

- **Status:** proposed
- **Date:** 2026-08-01
- **Issue:** #515
- **Decision:** `meterd` bills a per-app GB-h floor when
  `ScalingPolicy.MinInstances > 0` and the live instance count
  is below the floor. The sampler appends synthetic
  `usage_minutes` rows through the existing `AppendUsage` path
  with deterministic **UUID v5** instance IDs derived from a
  project-wide frozen namespace. Floor applies from t=0
  (the reaper's first-minute warm-up window is billable).
  Decimal-vs-binary GB-h consolidation is deferred to ADR-061.

## Why

Issue #462 closed (PR #493 / #501 / #507 / #512). The customer-facing
scaling-policy surface landed: Hobby customers can now configure
`min_instances > 0` and the reaper keeps that many instances warm
(`pkg/sched/reaper.go:114-179`). The single acceptance gap that
didn't ship: `meterd` bills only what the sampler observed. A Hobby
customer on `min_instances: 1` keeps a 264 MB instance resident
24/7, but `/v1/usage` reports `$0` until traffic arrives — a
write-only knob that violates the model's "predictable bills"
invariant (CLAUDE.md) and the Hobby break-even row in PR-A's
financial-model addendum.

This ADR is the closing seam. It does not widen the customer-facing
surface (the surface is on main from PR-A); it makes the existing
surface observable in billing.

## Decision

**1. Floor applies per-(app, minute) when `ScalingPolicy.MinInstances > 0`
   AND live `< min_instances`.** The reaper keeps the floor warm;
`meterd` bills it. Floor applies from t=0: the customer's bill
starts the moment they configure `min_instances > 0`, including
the reaper's first-minute warm-up window. Matches the Hobby
break-even row in PR-A's financial-model addendum ("customer pays
for the warm slot from the moment they configure it"). The floor
continues while a deployment is live or progressing toward live.
Once no live replacement exists and the latest deployment is terminal
(`failed | superseded | cancelled`), schedd cannot provide the configured
capacity and synthetic billing stops.
Sampler emits unconditionally for serviceable/in-flight deployments — no
opt-out flag in v1.

**2. Synthetic rows go through the existing `AppendUsage` path.**
Storage shape is unchanged: `usage_minutes` carries one row per
`(instance_id, minute)`, with `mb_seconds` first-write-wins on the
PK. The first-write-wins contract is load-bearing: a redelivered
minute cannot double-append.

**3. Synthetic instance IDs are deterministic UUID v5** —
`FloorInstanceID(appID, i) = uuid.NewSHA1(FloorNamespace, []byte(appID + ":" + i))`.
The `usage_minutes.instance_id` column is UUID
(`migrations/00001_init.sql:99`, PK `(instance_id, minute)`), and
`PgStore.AppendUsage` passes the ID raw — a non-UUID string fails
INSERT with `22P02 invalid_text_representation`. UUID v5 is the
ONLY synthetic-ID scheme that:
- passes the schema type check;
- preserves first-write-wins idempotency across re-ticks (the ID
  is a pure function of `(appID, i)`);
- is reproducible in tests (no time / random inputs).

**4. `FloorNamespace` is a project-wide frozen constant:**
`uuid.NewSHA1(uuid.NameSpaceURL, []byte("onebox-faas/meterd/floor/v1"))`.
The version suffix `v1` exists for a reason: rotating the
namespace changes every existing floor row's identity and breaks
`AppendUsage` idempotency across the upgrade. Any future rotation
MUST be a new namespace string (`v2`) plus a one-shot migration
that re-keys existing floor rows. Do not bump `v1` casually.

**5. Synthetic rows carry zero additive columns** — `cpu_usec`,
`tx_bytes`, `net_tx_bytes`, `net_rx_bytes`, `cold_boot_count`,
`requests`. Only `mb_seconds` is non-zero. Matches the free-tier
"instance just parked, no traffic yet" shape. `RolledRow`
gains a `SyntheticFloor bool` field as the in-memory lineage
marker (UUID lineage is opaque; the bool is the only
distinguishable signal for `Loop.runTicks` to count + emit).

**6. Floor total distributed across `(min_instances - live)`
synthetic rows.** Each row carries
`MBSecondsPerMinute(api.BillableRAMMB(app.RAMMB))` (= 264 × 60 =
15_840 for Hobby). Remainder of the integer division goes to
slot 0 so the sum equals exactly
`gap × BillableRAMMB × 60`. The downstream per-instance SQL
shape is preserved — `usage_minutes` still has one row per
synthetic instance per minute.

**7. New closed-set Prometheus counter `meterd_floor_applied_total{plan}`**
mirrors the `BillingCapExceededTotal` precedent exactly
(`pkg/wire/metrics.go`). Emit from `Loop.runTicks` after a
successful `SampleAndRoll`: count `SyntheticFloor == true`
rows from the returned `[]RolledRow`, group by appID, look up
plan via the tick-loaded `appID → plan` map (no extra
`ListAllApps` walk), `Inc()` once per affected `(app, tick)`.
Label set is `{plan}` only — closed-set via
`api.Plans = []Plan{PlanFree, PlanHobby, PlanPro, PlanScale}`;
`app` is unbounded cardinality and stays out.

**8. Decimal-vs-binary GB-h consolidation DEFERRED to ADR-061.**
The floor writes integer `mb_seconds`; both consumers
(`pkg/meter/math.go::GBHours` binary,
`cmd/apid/handlers_ext.go:1853` `usageSummary` decimal) get the
same value and convert as today. ADR-060 documents only.
ADR-061 (separate PR) consolidates the canonical convention.
Floor test assertions match each consumer's native convention.

**9. Free plan never sees the floor.** PR-A's
`CodePlanMinInstancesNotAllowed` gate rejects `min_instances > 0`
on PATCH for Free. Legacy Free apps from before PR-A carry
`min_instances = 0`; floor is silent. The gate is
defense-in-depth — the floor branch is reachable only when
`MinInstances > 0`, which Free cannot set.

## Failure modes

| Scenario | Behaviour |
|---|---|
| Cold-start with live = 0 and stamped floor | Floor emits `MinInstances` synthetic rows for that minute. Customer pays for the warm slot from t=0. |
| No live replacement and latest deployment is terminal | Floor emits no synthetic rows. Unserviceable capacity is neither retried nor billed. |
| Redelivered minute (AppendUsage with same `(instance_id, minute)`) | `mb_seconds` is `DO NOTHING` on the PK — second write is a no-op. The `cpu_usec / tx_bytes / ...` columns are additive, but synthetic rows carry zeros there. |
| Free PATCHes `min_instances=1` | `CodePlanMinInstancesNotAllowed` 4xx (PR-A's gate). Floor never fires for Free. |
| Legacy Free app with `min_instances=0` | Floor silent (zero policy). |
| `ScalingPolicy == nil` (legacy row pre-PR-A) | `ScalingPolicyOrDefault` returns zero-value; floor silent. |
| Sampler panic / context cancel mid-emit | Live loop is fail-fast; if it errors, the floor block does not run. A single minute of "no floor" is preferable to "floor without live rows" (visible as a usage spike). |
| `FloorNamespace` bumped (rotation) | Every existing floor row's identity changes; `AppendUsage` second-writes fail to idempotently match. Pinned by a doc comment + the namespace-version suffix. Future rotation requires new namespace + migration. |
| Synthetic UUID collision against real instance UUIDs | Real IDs are UUID v4 under `uuid.NewString()`; floor IDs are UUID v5 under `onebox-faas/meterd/floor/v1`. The SHA-1 inputs are disjoint, so the resulting UUIDs cannot collide. Pinned by `TestFloorInstanceID_PassesPgStoreAppendUsage`. |
| Clock skew between schedd and meterd (chrony on the reference node) | Floor is unconditional on the minute key (`MinuteKey(s.now())`); skew does not affect the floor math. |
| `Loop.runTicks` errors after `SampleAndRoll` returns rows | Floor emit runs AFTER `runSampleOnce` returns; emit happens only on a successful sampler tick. Failure path is unchanged. |
| `appPlan(appID)` lookup misses an app that was deleted between `ListAllApps` and the loop closure | Lookup returns `""` (empty plan). Counter emits with `plan=""`. Pinned by the `appPlan` helper treating a missing app as `""` rather than panicking. |

## Security

- **No widening of §11.** No new cgroup / uid / netns surface;
  meterd is non-root. Synthetic rows are pure Postgres INSERTs
  through the existing `AppendUsage` path; no new auth surface.
- **Synthetic IDs are derived strings**, not user input. No
  injection vector. The namespace string is project-wide constant.
- **Quota enforcement unchanged.** `pkg/meter/quota.go::EnforceQuota`
  reads `MonthlyUsageGB(usages)` from the rows; the floor inflates
  the sum automatically. Hobby/Pro/Scale use the "warn" path
  (not hard-stop), so the customer sees the overage warning —
  correct customer-facing behavior for a configured floor.
- **Free plan guardrail.** PR-A's PATCH-time rejection is the
  primary defence; the sampler's `MinInstances > 0` gate is
  the secondary. A Free app reaching the floor branch is a
  regression in PATCH enforcement and would emit a usage row —
  loud failure mode, silent bypass is impossible.

## Consequences

- **`pkg/meter/sampler.go`** gains `FloorNamespace` (frozen
  package var) + `FloorInstanceID(appID, i)` helper;
  `RolledRow` gains `SyntheticFloor bool`; `SampleAndRoll`
  appends `gap = MinInstances - live` synthetic rows after
  the live-instance loop.
- **`pkg/wire/metrics.go`** gains `meterdFloorAppliedTotal
  *prometheus.CounterVec` (5-tuple change: declaration,
  constructor, commonCollectors, pre-instantiation for
  `api.Plans`, struct field, nil-safe accessor).
- **`pkg/meter/loop.go`** gains the `appID → plan` map built
  in `runTicks`; `runTicks` emits
  `MeterdFloorAppliedTotal(plan)` once per app with synthetic
  rows after a successful `SampleAndRoll`. Counter increment
  one-per-(app, tick), not per synthetic row.
- **`pkg/meter/sampler_test.go`** gains the table-driven
  `TestSampler_AppliesMinInstancesFloor` (8 cases + 2 UUID-v5
  cases). **Mirrors** the existing `seedMinuteUsage` helper.
- **`pkg/meter/meter_sampler_redelivered_test.go`** gains
  `TestSampler_FloorAppliedAcrossMinuteBoundary` +
  `TestFloorInstanceID_DeterministicAcrossCalls` +
  `TestFloorInstanceID_PassesPgStoreAppendUsage` (the latter
  gated `//go:build pgtest`).
- **`pkg/meter/pusher_shadow_test.go`** updates existing 24h
  shadow tests to seed `MinInstances: 0` explicitly; adds
  `TestPushHour_Shadow24h_WithMinInstancesFloor`.
- **`cmd/e2e/meterd_floor_e2e_test.go`** (new) — Hobby app
  with `MinInstances=1, RAMMB=256`, zero live instances, one
  meterd tick, assert `GET /v1/usage` reports
  `mb_seconds == 15_840`.
- **No new migration.** `app.ScalingPolicy.MinInstances` is
  already on main (`migrations/00082_apps_scaling_policy.sql`).
  Slot 83+ remains open but unused.
- **No SDK / OpenAPI / proto surface widening.** `GET /v1/usage`
  shape is invariant; floor rows are indistinguishable from live
  rows on the wire except via the synthetic UUID lineage
  (opaque to the SDK).
- **No `pkg/api/limits.go` change.** Floor is bounded by
  `MinInstances` (already DTO-validated by PR-A's
  `ErrInvalidMinInstances`); no new quota surface.

## Rejected alternatives

- **Reaper-side emission** — wrong place. The reaper is a
  per-instance state machine (`pkg/sched/reaper.go:114-179`),
  not a per-app minutes aggregator. Reaper → meterd is a
  pull boundary (`Store.AppendUsage`), not push.
- **Background goroutine in `Loop`** — races the sampler;
  complicates test clock injection; partial-failure modes
  (floor emitted without live rows, or vice versa) become
  observable.
- **Bumping live-row `AdmissionMB`** to absorb the floor —
  breaks the per-instance SQL aggregation shape (`Σ
  AdmissionMB` no longer equals `live_count × billable`).
  Confuses the dashboard "instances" chart.
- **Two-pass aggregation in `aggregator.go`** — requires
  per-app rollup state the current `UsageByMonth` SQL does
  not return. Sampler-side append is O(1) per app; aggregator-
  side two-pass is O(apps) per minute.
- **Per-account floor** (`Σ min_instances` across all apps in
  an account) — loses the customer-visible per-app contract.
  `min_instances` is a per-app knob; the floor must be too.
- **Persisting `app.billable_minutes` column** — premature.
  The integer `mb_seconds` already encodes the floor; a
  dedicated column adds migration + sync overhead for no
  operational gain.
- **Literal `<appID>:floor:<i>` strings** — fails `22P02
  invalid_text_representation` on INSERT (UUID column type).
  Rejected by the schema.
- **UUID v7 (time-ordered)** — not deterministic across
  re-ticks; breaks first-write-wins on `(instance_id, minute)`.
  Requires a separate idempotency key in a side table.
- **Per-plan Prometheus label beyond `{plan}`** — adds a
  second `ListAllApps`-equivalent scan in the Loop closure
  for an unbounded-cardinality `app` label. Closed-set
  precedent (`BillingCapExceededTotal`) is `{plan}` only.
- **Quota-tiered floor (e.g., Hobby discount)** — financial-
  model addendum holds; out of scope for the closing seam.
- **Global kill-switch flag** (`--disable-floor`) — the
  `MinInstances > 0` gate is the only switch needed. A
  process-level kill-switch is a separate ops-tooling
  follow-up.

## Downstream

- **Issue #515 closes.** This PR merges the meter-side floor;
  `min_instances > 0` becomes a billing-observable knob.
- **PR-A follow-ups (out of scope here):**
  - **ADR-061 (proposed):** consolidate the decimal-vs-binary
    GB-h convention between `pkg/meter/math.go::GBHours` and
    `cmd/apid/handlers_ext.go:1853` `usageSummary`. Pick one
    canonical divisor (binary or decimal) and migrate both
    consumers. Separate PR; small blast radius.
  - **ADR-062 (proposed):** dashboard "floor applied" pill
    driven by `meterd_floor_applied_total{plan}` rate
    (per-plan breakdown for ops visibility).
  - **Future:** per-app `WorkloadClassWorker` `MinInstances`
    API surface. Workers are reaper-exempt today (PR-D) but
    the customer-facing surface for `min_instances > 0` on a
    worker-class app is a v2 story.

## Reused on main (no redesign)

- `state.ScalingPolicy` (`pkg/state/types.go:324-356`) and
  `ScalingPolicyOrDefault(*ScalingPolicy) ScalingPolicy`
  helper (`pkg/state/types.go:413-422`).
- `state.UpdateAppParams{ScalingPolicy *ScalingPolicy;
  SetScalingPolicy bool}` (`pkg/state/types.go:1238-1243`)
  — PATCH-time write path. Test seam:
  `pkg/sched/engine_test.go:338-359` `setAppPolicy` helper.
- `pkg/state/store.go::AppendUsage` 11-arg signature
  (`pkg/state/store.go`); `mb_seconds` first-write-wins on
  `(instance_id, minute)`; additive columns are
  `cpu_usec / tx_bytes / net_tx_bytes / net_rx_bytes /
  cold_boot_count / requests`.
- `api.BillableRAMMB(ramMB) = ramMB + PerVMOverheadMB`
  (`pkg/api/limits.go:1232-1240`); `PerVMOverheadMB = 8`
  (`pkg/api/limits.go:589-591`).
- `pkg/meter/math.go::MBSecondsPerMinute(admissionMB)
  = admissionMB × 60` (`pkg/meter/math.go:28-30`).
- `pkg/wire/metrics.go::BillingCapExceededTotal`
  (declaration `:372-380`, constructor `:733-736`,
  commonCollectors `:1013`, pre-instantiation
  `:1180-1182`, struct field `:1303`, accessor
  `:2102-2114`) — the closed-set counter precedent.
- `pkg/sched/reaper.go:114-179` — reaper-side floor
  enforcement. PR-D shipped worker carve-out
  (`pkg/sched/reaper.go:170`); meter-side floor is
  the symmetric closing seam.
- `pkg/meter/aggregator.go::MonthlyUsageGB` and
  `CheckQuota` (`pkg/meter/aggregator.go:19-27, 54-73`) —
  both read `mb_seconds` from the rows and convert
  via `GBHours` (binary). No change.
- `pkg/meter/pusher.go::PushHour` (`pkg/meter/pusher.go:104-169`)
  — sums `mbSec` across all per-app rows; floor inflates the
  per-account sum automatically.
- `cmd/apid/handlers_ext.go::usageSummary` (`:1853` decimal
  divisor) and `cmd/apid/handlers_ext.go::getUsage`
  (`:1097-1145`) — both consume `mb_seconds` from the same
  rows; convention-independent.
- `google/uuid` library (already in `go.mod`; used elsewhere
  via `google/uuid.NewString()`).
- `pkg/meter/sampler.go::Sampler.SampleAndRoll` (the existing
  tick function; PR-A adds the floor append block, no signature
  change).

## Financial-model addendum (PREREQUISITE)

Per CLAUDE.md ("financial model is source of truth"), the
financial model spreadsheet's Hobby break-even row needs a
`min_instances: 1` scenario column BEFORE this PR merges. The
column should pin:

- Hobby `min_instances: 1` × 24 × 30 = 720 instance-hours
  resident; at `BillableRAMMB(256) = 264` × 60s × 720 =
  11_404_800 mb_seconds/day → ~3.17 GB-h/day → ~95 GB-h/month
  at the binary divisor (or ~3.17 GB-h/day → 95 GB-h/month
  decimal).
- Verifies the floor inflates Hobby's bill from 50 GB-h
  included → ~145 GB-h billable at `min_instances: 1`, with
  the 95 GB-h overage at the model's €0.01/GB-h rate.
- Confirms the Hobby break-even row's "predictable bills"
  invariant holds: the customer knows the floor cost before
  they set the knob.

The addendum is informational (the math is integer and exact);
no implementation change depends on it. But CLAUDE.md is
load-bearing — the PR cannot merge until the spreadsheet row
is committed to the reference node.

## Verification

Unit tests (no KVM):

- `pkg/meter/sampler_test.go` —
  `TestSampler_AppliesMinInstancesFloor` (table-driven, 8
  cases + 2 UUID-v5 cases).
- `pkg/meter/meter_sampler_redelivered_test.go` —
  `TestSampler_FloorAppliedAcrossMinuteBoundary` (idempotency
  across redeliveries + minute boundaries),
  `TestFloorInstanceID_DeterministicAcrossCalls` (UUID v5
  purity),
  `TestFloorInstanceID_PassesPgStoreAppendUsage` (gated
  `//go:build pgtest`, proves the schema round-trip).
- `pkg/meter/pusher_shadow_test.go` —
  `TestPushHour_Shadow24h_WithMinInstancesFloor` (24h
  integer equality with floor inflation).
- `pkg/wire/metrics_test.go` — assert
  `meterdFloorAppliedTotal` is in `commonCollectors` and
  pre-instantiated for all `api.Plans`.

Integration:

- `make test` — unit suite, must pass.
- `make lint` — golangci-lint + custom checks.
- `make gen` / `make sdk-check` / `make spec-check` —
  NOT needed. Wire shape is invariant; no proto, SDK,
  OpenAPI surface change.

E2E:

- `cmd/e2e/meterd_floor_e2e_test.go` —
  `TestMeterdFloor_GetUsage_BillsSyntheticRows`
  (mirror of `cmd/e2e/egress_metering_test.go:51-183`):
  boot meterd + apid, seed Hobby app with
  `MinInstances=1, RAMMB=256`, zero live instances, one
  meterd tick, assert `GET /v1/usage` reports
  `mb_seconds == 15_840`.

Smoke:

- Scrape `meterd_floor_applied_total{plan="hobby"}` on a Hobby
  cluster with `min_instances=1`; expect non-zero within one
  minute of a Hobby warm-up.
