# ADR-098 · Wake single-flight coordinator on `sched.Engine`

- **Status:** accepted (merged as PR #854 / commit 93059ff4)
- **Date:** 2026-08-11
- **Decision:** Hoist the per-app "wake in progress" coordination out of
  `pkg/gateway/WakeGate` (per-process in `gatewayd-internal`) and into
  `pkg/sched.Engine.wakeCoord`. Add an additive `schedd.EnsureWake` gRPC
  method. Route every wake producer — gateway, cron, floor, scaleup, targets
  — through `Engine.EnsureWake`. The per-process `WakeGate` remains as an
  in-process pre-filter (a cache, no longer the authority).

## Why

Today only the gateway coalesces wake requests, and only within a single
`gatewayd-internal` process (`pkg/gateway/gate.go`). Cron
(`pkg/sched/loop.go:1969`), floor (`pkg/sched/floor/trigger.go:505,620`),
scaleup (`pkg/sched/scaleup/trigger.go:384`) and targets
(`pkg/sched/targets/trigger.go:293`) each independently call
`Engine.Wake` / `Engine.AdmitInstance`. A burst hitting the same parked
app simultaneously through all four triggers boots one VM per caller; the
4-phase `appMu` discipline in `pkg/sched/engine.go:911-1924` tears down the
losers in Phase 4 (`engine.go:1818, 1823, 1830-1831, 1435`).

Cost:

- Transient RAM overshoot above the §6.2-2 ceiling (`47,600 MB`), since
  all callers have already passed the ledger admit check before they see
  one another.
- Lost snapshot-read moments — two callers can race past the
  Phase-1 fast path (`engine.go:950-1017`) without one seeing a `RUNNING`
  row, then both proceed to restore.
- VM churn burns cgroup setup / teardown cycles that the 350 ms wake
  budget cannot afford.

After the audit: 10 enumerated correctness risks tied to this surface
(`pkg/gateway/gate.go:88-217`), including double-commit on leader loss,
stale wake after app eviction, gateway ctx propagation, and the
Phase-1 fast-path bypass.

## Decision

Place per-app "wake in progress" state on the `Engine`. Mirror
`pkg/gateway/gate.go`'s `done` / `waiters` / `completed` shape but with
the lock discipline inverted: `wakeCoord.mu` is a leaf, acquired **and**
released **before** `e.lockApp(appID)` is touched. The Phase-1..4
`appMu` window is not widened, narrowed, or re-entered.

Add an additive `schedd.EnsureWake(ctx, appID) → (RunningInstance, error)`
gRPC method (`api/proto/onebox/faas/schedd/v1/schedd.proto`); mirrors the
existing `AdmitInstance` request shape and the `WakeResponse` tag layout
1–7. All five wake producers call `Engine.EnsureWake`; `WakeGate` becomes
a cache in front of the new authority, not the authority itself.

Decrement discipline: a single `defer` closure at leader entry captures
the completion site for every wake. Internal `sync.Once` `finish()` is
the belt-and-braces — no hand-placed decrement at any of the five
completion sites.

Eviction: a new `pkg/db.NotifyAppDelete` pg-notify channel and a new
`pkg/sched/app_delete_subscriber.go` (modelled line-for-line on the
existing `pkg/sched/deletion_subscriber.go`) call `Engine.wakeCoord.Forget(appID)`
on row removal. The subscriber takes only `wakeCoord.mu` and never
touches `appMu`. The leaf-lock rule is the load-bearing invariant.

## Invariants preserved (4)

1. **Cold boot remains truth (§4.6).** `EnsureWake` never depends on a
   snapshot being usable. A leader that can't restore a snapshot falls
   through to cold boot **inside** its single-flight window — followers
   see that same boot, not their own.
2. **Wake never depends on snapshot presence (ADR-005).** Missing or
   stale snapshot → cold boot. Unchanged.
3. **Inner net identical (ADR-009).** The coordinator never reuses a
   netns / TAP / IP across winners. One snapshot can still restore as N
   distinct instances; N wakes for the same snapshot now collapse to 1.
4. **Admission ceiling preserved (§6.2-2).** `EnsureWake` serialises in
   front of admission; the ledger stays the authority. The
   `Σ(ram_mb + 8) ≤ 47,600 MB` invariant is not relaxed.

## Detached-ctx contract

The leader's `ensure` runs on `context.Background()` + `WakeQueueTTLSeconds`
(`pkg/api/limits.go:1567`, 30 s), not the caller's `ctx`. A cancelled
follower cannot kill an in-flight boot. `Forget(appID)` closes `done`
with `ErrAppDeleted` so followers unwind promptly.

## Consequences

- **New `pkg/sched/wake_coord.go`** mirroring `pkg/gateway/gate.go`'s
  state-machine shape but on the `Engine`. Whitebox test
  `pkg/sched/wake_coord_test.go` (`package sched`) per memory
  `whitebox-test-file-pattern.md`.
- **Property test `pkg/sched/invariants_property_test.go`** (already in
  the repo at the §6.2-1 acceptance shape per memory
  `tier-a-e2e-shape.md`); C5 flips it from "every call boots a VM" to
  "exactly one BootVM per concurrent burst, no leaked reservations,
  post-leader-loss followers see error".
- **New `pkg/sched/app_delete_subscriber.go`** for `db.NotifyAppDelete`.
- **New RPC `schedd.EnsureWake`** + additive `AdmitInstanceResponse.request_count`
  field at tag 9.
- **Per-instance `request_count` column** on `instances` (migration at
  slot 00221, originally 00216 fence / 00217 real — renumbered after
  main absorbed 00215/00216 on 2026-08-13; the fence was dropped
  per memory `cross-pr-rebase-fence-deletion-hazard`); the
  warm-snapshot 5th promotion gate (gate #5 after
  `engine.go:3870-3875` MinMs gate) reads from this column.
- **`gateway_leader_bootstrap_aborts_total{reason}`** counter on the
  gateway's existing per-handler registry, sibling of `gateway_wake_latency_seconds`.
  Three pre-instantiated closed reasons: `queue_empty_no_instance`,
  `ttl_expired`, `app_deleted`.
- **No breaking change to wire shape.** `AdmitInstance` stays. All proto
  additions follow the ADR-016 additive convention (plain scalars with
  zero-value-means-unset, no `optional` keyword).
- **No quota / limit change.** `pkg/api/limits.go` is unchanged. The
  512/30 s `WakeQueueCap` / `WakeQueueTTLSeconds` still bound the
  pre-filter `WakeGate`; the coordinator inherits the same TTL.

## Amendment 1 — Cluster-coord primitive added (multi-host safety cluster PR-5)

ADR-098's original "rejected: pg_advisory_lock per app_id" reasoning
was correct for the single-box posture — the in-memory
`wakeCoord` is amortised to zero additional wire hops under
steady state, and adding a PG round-trip to every wake call was
net negative. Multi-host changes the cost model: two schedds on
different boxes each spin up their own `wakeCoord`, and a
fan-out event (cron event broadcast, gateway retry storm) can
land the same `wake_id` on both schedds in the same instant.
The in-memory coordinator is invisible across boxes — its
serialisation guarantee stops at the process boundary.

PR-5 (audit F4) layers a DB-side cluster-coord primitive on top
of the in-memory coordinator:

- **Layer 1 (owner gate at EnsureWake entry)** is an ADR-062
  amendment (see `062-tier-a-per-node-schedd-and-placement.md`
  Amendment 1). It catches the dominant case: a wrong-box wake
  fails-fast before consuming a `wakeCoord` slot.

- **Layer 2 (DB-level partial unique index)** is this ADR's
  amendment. Migration `00350_instances_wake_attempt_active_unique.sql`
  creates:
  ```sql
  CREATE UNIQUE INDEX instances_wake_attempt_active_idx
      ON public.instances(wake_id)
      WHERE state IN ('WAKING', 'COLD_BOOTING');
  ```
  The partial predicate is the load-bearing piece: a row whose
  state has flipped to `RUNNING` / `PARKING` / `PARKED` falls
  outside the index, so a re-wake of the same logical request
  (gateway retry after long pause, cron event re-delivery) can
  still INSERT. Only the simultaneous-flight race is rejected.

  `Store.CreateInstance` (pgstore.go:8494) translates SQLSTATE
  `23505` into the typed sentinel `state.ErrConcurrentWake`;
  `pkg/sched.Engine.createInstanceWithWakeRetry` (engine.go:~810)
  wraps the three `CreateInstance` call sites in a 3-attempt
  jittered loop (50-200ms) that recovers via
  `Store.ReadActiveInstanceForWakeID` when it loses the race.

The in-memory `wakeCoord` remains the single-box fast path. The
DB-level index is the cluster-coord primitive. The two are
deliberately redundant: Layer 1 short-circuits the wrong-box
case before any queue/DB work; Layer 2 catches the same-box-but-
still-racing case (e.g. an HPA controller sending a burst, a
stale leader election) and the cross-box case Layer 1 cannot
catch (two schedds racing on a not-yet-claimed app).

New surfaces:
- `pkg/state.ErrConcurrentWake` sentinel.
- `pkg/state.Store.ReadActiveInstanceForWakeID` (pgstore + memstore mirror).
- `pkg/sched.Engine.createInstanceWithWakeRetry` (the 3-attempt retry helper).

New tests:
- `pkg/state/pgstore_test.go::TestPg_CreateInstance_PartialUniqueIndexBlocks`
- `pkg/state/pgstore_test.go::TestPg_CreateInstance_PartialUniqueIndex_AllowsAfterPark`
- `pkg/state/pgstore_test.go::TestPg_ReadActiveInstanceForWakeID_ReturnsWinner`
- `pkg/state/pgstore_test.go::TestPg_ReadActiveInstanceForWakeID_ParkedRowHidden`
- `pkg/sched/engine_test.go::TestEngineEnsureWake_RefusesForeignOwnedApp`
- `pkg/sched/engine_test.go::TestEngineEnsureWake_AllowsOwnerSameBox`
- `pkg/sched/engine_test.go::TestEngineEnsureWake_AllowsUnownedApp`

The cross-PR e2e pin (`cmd/e2e/fleet_wake_dedup_e2e_test.go`,
build tag `metal`) covers the full two-schedd-two-gatewayd
fleet scenario end-to-end.

## Amendment 2 — Loser surfaces sentinel, never returns the winner row (correction)

The original Amendment 1 described the helper as a 3-attempt
jittered loop that "recovers via `Store.ReadActiveInstanceForWakeID`
when it loses the race." Code-review agent #1036 (PR #1036
verify pass) found that this recovery branch is the ship-
blocking shape: returning the winner's `state.Instance` to the
loser causes the engine downstream — which is keyed by
`(ins.ID, placement.NodeID)` — to boot a LOCAL microVM tagged
with the REMOTE winner's instance UUID. Six concrete failure
modes follow (wrong node tag, double-counted per-app
concurrency, local VM with remote UUID, `HostIP` clobber race,
`store.DeleteInstance` deleting the winner on the loser's
failure path, `transitionWithKind` marking the winner `FAILED`).
In the single-box degenerate case (two schedds on one node, no
`App.NodeID` claim yet) the two would boot on the same node —
colliding on cgroup / jail uid / netns in violation of spec
§6.2-5.

The corrected contract (one attempt, no jitter, no winner-row
read):

1. `pkg/sched.Engine.createInstanceWithWakeRetry` calls
   `store.CreateInstance` exactly once. The partial unique
   index is binary (succeeds or `23505`) — retries against the
   same `wake_id` cannot win because the winner's row is
   already in the index's `WAKING` / `COLD_BOOTING` predicate.
   The 3-attempt jittered loop was structurally useless and
   has been removed.

2. On `23505`, the helper returns `(state.Instance{},
   state.ErrWakeAlreadyInflight)` — NOT the winner's row. The
   three `CreateInstance` call sites (engine.go:1460 floor
   admit, :1764 wake dispatch, :3750 prime) propagate the
   sentinel as a typed error via `fmt.Errorf("...: %w", err)`.
   The engine's downstream (ledger.Admit, vmm.CreateColdBoot,
   SetInstanceRuntime, store.DeleteInstance,
   transitionWithKind, emitInstanceChanged) never runs on the
   loser path; no local microVM is ever booted with the winner's
   instance UUID.

3. Callers that need to observe the winner's progress (gateway-
   side retry poll, cron-side reschedule, redeploy follow-up)
   call `Store.ReadActiveInstanceForWakeID` directly — the
   primitive remains exported for that purpose. The helper no
   longer auto-recovers.

4. The `pg_notify` wake-coord follower path (the wakeCoord
   `call.Await` block at engine.go:1254) inherits the same
   `ErrWakeAlreadyInflight` from the leader via `CoordOutcome.Err`
   — followers exit cleanly without attempting local boot.

New surfaces:
- `pkg/state.ErrWakeAlreadyInflight` typed sentinel (the engine
  helper surfaces it; the raw `ErrConcurrentWake` stays as the
  store-layer sentinel).

New tests:
- `pkg/sched/engine_test.go::TestEngine_CreateInstanceWithWakeRetry_LoserSurfacesSentinel`
  pins the helper contract: store returns `ErrConcurrentWake`,
  helper returns `ErrWakeAlreadyInflight` with empty `Instance`;
  `CreateInstance` was called exactly once.

The cross-PR e2e pin (`cmd/e2e/fleet_wake_dedup_e2e_test.go`,
build tag `metal`) was updated: the loser is now expected to
surface `ErrWakeAlreadyInflight` (or the raw `ErrConcurrentWake`
from the store, both are accepted), and the assertion "both
schedds observe the same winner via the retry helper" was
removed — the loser now exits cleanly with an empty
`Instance`.

## Amendment 3 — Demand-aware cold-burst fan-out with a live-capacity baseline

Strict single-flight protects a parked app from duplicate boots, but one cold
instance cannot absorb every burst. PR #1300 allowed additional coordinator
leaders when queued demand exceeded the capacity of in-flight wakes. Its rule
ignored instances that were already live. In production, 50 concurrent requests
against an app with 16 running instances started unnecessary wakes and regressed
responses to 30 HTTP 200s, 19 HTTP 504s, and one HTTP 503. PR #1301 reverted the
change.

The corrected rule counts both existing instances and wakes started by the
current coordinator generation:

```text
capacity = existing_at_generation_start + in_flight_wakes
fan_out = waiting > capacity * concurrency_per_vm
          && capacity < max_concurrency
```

`existing_at_generation_start` comes from `NodeLedger.Concurrency(appID)`, which
counts `RUNNING`, `WAKING`, and `COLD_BOOTING` reservations. The first leader
captures that value for the generation, and every sibling reuses it. This detail
prevents a fresh ledger read from counting the generation's own reservations once
as existing capacity and again as coordinator wakes.

The app's `max_concurrency` and the plan's `ConcurrencyPerVMBound` are cached for
15 seconds to keep app and account reads off the burst hot path. The ledger count
is read on every call and is never cached. A lookup failure disables fan-out for
that call; the ledger remains the final admission authority and continues to
enforce the application and node ceilings.

Followers join the least-loaded active wake. The 512-caller cap still applies per
wake, and app deletion completes every active wake with `ErrAppDeleted`. The
gateway's local burst admission remains complementary: it sees a single
gateway's request pressure, while this coordinator safely combines gateway,
cron, floor, scaleup, and targets producers.

Regression coverage:

- `TestWakeCoord_ExistingCapacityPreventsWarmBurstFanout` pins the production
  shape: 16 existing instances at 80 requests per VM absorb 200 callers without
  creating sibling coordinator leaders.
- `TestWakeCoord_FansOutWhenDemandExceedsOneInstance` proves a cold generation
  still starts a second wake even after the first reservation appears in the
  ledger snapshot.
- `TestEngineWakeFanoutForRefreshesExistingFromLedger` proves only static policy
  is cached and the live ledger count is refreshed.

## Rejected alternatives

- **Extend `pkg/gateway/WakeGate` to cover cron / floor / scaleup / targets.**
  Rejected: inverts the spec's ownership model (`schedd` is sole writer
  to `instances`; `gatewayd-internal` is the HTTP edge). Cron / floor
  / scaleup / targets would all have to round-trip through gatewayd for
  each call, adding latency to the §14 cold-boot budget.
- **A `pg_advisory_lock` per app_id.** Rejected: every wake calls into
  the ledger admit anyway (one PG round-trip). Adding a second
  round-trip to wake is wasted; the in-memory coordinator amortises to
  zero additional wire hops under steady state.
- **Defer the coordinator to a separate PR.** Rejected: ADR-098 is on
  the critical path for the §6.2-1 property test. It belongs with the
  producers' switchover (C5) and the vmmd-internal phase telemetry
  (C11) in one PR-cluster.

## Risks + mitigations

- **Double / missed decrement across the five completion sites.** Mitigation:
  one deferred closure at leader entry + `sync.Once` `finish()` as
  belt-and-braces. No hand-placed decrement at any loser branch.
- **Lock-order inversion `wakeCoord.mu` ↔ `appMu`.** Mitigation:
  leaf-lock rule documented at the top of `wake_coord.go`; `-race` on
  the property test; review-checklist item.
- **Detached-context goroutine leak.** Mitigation: `leakcheck` on
  C3 / C4 / C5 / C6 / C11.
- **Stale wake firing for an evicted app.** Mitigation:
  `pkg/sched/app_delete_subscriber.go` calls `Forget(appID)`; new
  `pkg/db.NotifyAppDelete` channel + subscriber modelled on
  `pkg/sched/deletion_subscriber.go`.
- **e2e p50 / p95 regression from the new RPC.** Mitigation: keep the
  in-process `WakeGate` pre-filter so the RPC fires once per burst;
  the "already live" fast path does not take `appMu`.

## References

- §4.6 two-drive rootfs; §6.1 wake state machine; §6.2 invariants;
  §14 acceptance tests; G7 connection protection.
- ADR-005 (snapshot cache, cold boot truth), ADR-009 (identical inner
  net), ADR-016 (additive proto wire), ADR-070 (gatewayd-public /
  gatewayd-internal split), ADR-074 (warm-snapshot audit + GC, the
  ADR-071 implementation that shipped), ADR-090 named envs
  (env-overlay wake pattern to mirror).
- `pkg/sched/engine.go:911-1924` (4-phase lock discipline),
  `pkg/sched/engine.go:4973-4980` (`lockApp` / `unlockApp`),
  `pkg/sched/framework_ready_at` path (PR #543 batched-writer
  precedent).
- Memory: `pgtest-pool-exec-vs-queryrow-for-selects`,
  `schedd-engine-lock-narrowing`, `wire-opsmetrics-single-registry`,
  `whitebox-test-file-pattern`, `cmd-e2e-paddle-sandbox-api-flake`.
