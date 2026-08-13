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
