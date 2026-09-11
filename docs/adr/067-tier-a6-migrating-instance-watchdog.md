# ADR-067 · Tier A6 migrating-instance watchdog

- **Status:** **Accepted** (revised 2026-08-16)
- **Date:** 2026-08-02
- **Decision:** Wire a 1 s schedd tick that self-heals stuck `state='migrating'`
  rows that never committed (the new owner vmmd died mid-handoff, the
  network partition dropped the gRPC, the operator killed the new owner
  before the phase-3 commit). The watchdog is the **only writer** that
  can move a row out of `migrating` without a peer commit — every Phase-4
  path (`CancelInstanceMigration`) requires a peer, and the peer is the
  very thing that's gone.
- **Why:** ADR-066 §"Open follow-ups" item 1. A persistent `state='migrating'`
  block is the realistic operational failure mode for the four-phase
  handoff: the lease eventually expires, the dying vmmd sees the
  expiry and resumes, but the DB row stays in `migrating` forever
  (none of the four Phase-4 paths fire — they all need a peer, and
  the new owner's gRPC died before the phase-3 commit). The row
  then blocks the next live-migration attempt (the conditional
  UPDATE on `state='running'` fails because the current state is
  `migrating`) and pins the customer's instance in an unkillable
  state. The only mechanism that can free it is a watchdog with a
  unconditional re-evaluation path.
- **Consequences:**
  - New per-tick metric `schedd_migrating_reconcile_total{outcome}`.
  - New `events` audit row kind `migration_reconciled` (reuses the
    existing `events` table; no new notify channel).
  - Two new conditional-UPDATE predicates on `instances`:
    `ReinviteMigratingInstance` (active-owner-ack path) and
    `AbortMigratingInstance` (dead-owner hard-delete path).
  - One additive column migration persists the Phase-2 start time so
    lease age remains durable across schedd restarts.

## Architectural decisions

1. **Trigger.** A 1 s `time.Ticker` in the existing schedd watcher
   loop (parallel to `runRetention` / `runScaleUp` /
   `runDiskDrift`). The watchdog is NOT notified by `pg_notify`
   because the failure mode is "the new owner died silently" —
   there is no peer to emit a notify.
2. **Input set.** `Store.ListExpiredMigrations(ctx, maxPerTick, leaseAge)`
   returns only `state='migrating'` rows whose persisted
   `migration_started_at` is at least `leaseAge` old. The one-second
   ticker remains responsive, but an in-flight handoff is protected from
   premature reconciliation even across a schedd restart. Race-safety
   still lives in the conditional-UPDATE predicates.
3. **Eligibility.** `state='migrating' AND lease_token IS NOT NULL`
   (every migrating row carries a lease token stamped at Phase 2;
   the watchdog needs the lease to drive the gRPC re-invite).
   Capped at `api.MigratingWatchdogTickLimit` per tick (default 50).
4. **Recovery decision.** Per-instance, the watchdog consults
   `compute_nodes.active` for the row's *current* `node_id`
   (the dying vmmd — Phase 2 didn't flip `node_id`; only Phase 3
   does):
   - **Active owner:** the dying vmmd is still up and accepting
     gRPC. The watchdog issues a re-invite (`AdoptMigratedInstance`
     with the same lease token) and bumps
     `outcome="reinvited"` on ack.
   - **Dead owner:** the dying vmmd is gone (or the row is from
     a peer-owner that drained). The watchdog issues the
     conditional-UPDATE `AbortMigratingInstance` that flips
     `state='parked'`, restores `node_id=migrated_from_node_id`
     (the live, parked state the row was in before Phase 2),
     and clears `lease_token`. Bumps `outcome="hard_deleted"`.
   - **Conflict (lease mismatch / row already gone):** peer
     race-winner already committed or rolled back. Bumps
     `outcome="conflict"` and drops silently.
   - **Error (transient transport / DB blip):** bumps
     `outcome="error"`, logs Warn, continues. The next tick
     retries.
5. **Race safety.** `ReinviteMigratingInstance` and
   `AbortMigratingInstance` are both conditional UPDATEs that
   require `state='migrating'` AND `lease_token=$leaseToken`.
   A peer that committed while the watchdog was thinking fails
   the predicate and bumps `outcome="conflict"` (peer-wins).
   A peer that rolled back while the watchdog was thinking
   also fails the predicate (state is now `parked`). The
   lease-token predicate is the load-bearing race-safety
   guarantee — mirrors the A5 phase-3 commit predicate.
6. **Audit.** Every reconciled row writes an `events` row with
   `kind='migration_reconciled'`, `app_id`, `instance_id`, the
   previous owner (`node_id`), the lease token, and the outcome
   (`reinvited` / `hard_deleted` / `conflict` / `error`). The
   events table is the operator's source of truth for "why did
   this instance get force-killed" — the existing
   `events.kind='instance_state_change'` audit doesn't cover
   the watchdog path because the watchdog does not go through
   the normal state-machine transition.
7. **Notification.** None. The watchdog writes to `events` only;
   it does NOT emit `NotifyInstanceChanged` (the gateway listener
   is keyed on per-instance state changes that flow through the
   normal state machine; the watchdog path is a recovery, not a
   customer-visible transition). A customer whose instance was
   force-killed by the watchdog sees a 503 next request and the
   reconciler drives a fresh wake — the gateway never learns
   about the reconciliation event.
8. **Cadence.** `MigratingWatchdogIntervalSeconds` (default 1 s;
   env override `FAAS_MIGRATING_WATCHDOG_INTERVAL_SECONDS`). The
   1 s default matches the §6.1 watchdog cadence; the migration
   watchdog is the second member of the same family.
9. **Per-tick cap.** `MigratingWatchdogTickLimit` (default 50;
   env override `FAAS_MIGRATING_WATCHDOG_TICK_LIMIT`). A flood
   from a single bad-owner event is bounded.
10. **Engine seam.** `Engine.ReconcileExpiredMigrations(ctx)` —
    the per-tick work function. The watchdog goroutine calls
    this; the engine reads the input set, walks it, and emits
    the per-instance metrics + audit rows.
11. **Watchdog struct.** Lives in `pkg/sched/migrating_watchdog.go`
    (parallel to `pkg/sched/rebalancer.go`, `pkg/sched/live_migrator.go`).
    `MigratingWatchdog.Run(ctx, ticker)` — the ticker drives the
    cadence; the watchdog calls
    `Engine.ReconcileExpiredMigrations(ctx)` on each tick.
12. **Wire.** None. All consumer changes are server-side; ADR-016
    holds.

## Configuration

| Env var | Default | Purpose |
|---|---|---|
| `FAAS_MIGRATING_WATCHDOG_TICK_LIMIT` | `api.MigratingWatchdogTickLimit` (=50) | Per-tick cap on the wedged-migration candidate set. |
| `FAAS_MIGRATING_WATCHDOG_INTERVAL_SECONDS` | `api.MigratingWatchdogIntervalSeconds` (=1) | Per-tick cadence. |

Constants live in `pkg/api/limits.go` (the canonical hard-limits
table; never inline a limit per CLAUDE.md).

## State surface

- New `Store.ListExpiredMigrations(ctx, maxPerTick int, leaseAge ...time.Duration) ([]Instance, error)`
  — returns leased `state='migrating'` rows older than the lease age.
  The optional form preserves the existing inspection surface; the
  production engine always passes the configured live-migration lease.
  Implements the existing `InstancingStore` interface; mirrored in
  `pgstore` and `memstore`.
- New `Store.ReinviteMigratingInstance(ctx, instanceID, leaseToken string) error`
  — conditional UPDATE that flips `state='running'`, stamps
  `migrated_at`, clears `lease_token`. Used by the active-owner
  ack path. Returns `ErrConflict` on `RowsAffected()==0`.
- New `Store.AbortMigratingInstance(ctx, instanceID, leaseToken string) error`
  — conditional UPDATE that flips `state='parked'`, restores
  `node_id=migrated_from_node_id`, clears `lease_token`. Used by
  the dead-owner path. Returns `ErrConflict` on `RowsAffected()==0`.
- **Migration.** `instances.migration_started_at` is stamped atomically
  with the Phase-2 `state='migrating'` transition and cleared by every
  terminal path. The existing A5 lineage columns remain unchanged.

## Engine surface

- `Engine.ReconcileExpiredMigrations(ctx)` — the per-tick work
  function. Reads the input set, walks it, dispatches to
  `Store.ReinviteMigratingInstance` / `Store.AbortMigratingInstance`
  (the active-vs-dead decision is pure PG: `compute_nodes.active`
  for the row's current `node_id`), bumps the metric, writes the
  audit row. Returns `(reconciled int, err error)`.
- `Engine.WithMigratingWatchdogTickLimit(n int)` / `Engine.WithMigratingWatchdogIntervalSeconds(s int)` —
  the per-engine overrides (same panic-on-bad-env contract as the
  A5 `WithMigrateLiveLeaseSeconds`).

## Notification kind taxonomy

- **No new channel.** `events` table only. The watchdog does not
  emit `NotifyInstanceChanged` (the customer-visible transition is
  the next wake, which goes through the normal state machine).
- The `events` row kind `migration_reconciled` is documented in
  `pkg/db/events.go` (the existing kind string `events.kind`
  column has a CHECK; no schema change needed).

## Runbook update

- `docs/runbooks/gate-a.md` §"Live-instance migration" — add a
  paragraph noting the watchdog reconciles wedged `migrating`
  rows within 1 s of the new owner vmmd dying. The §12 metric
  `schedd_migrating_reconcile_total{outcome="hard_deleted"}`
  is the operator's tripwire: a persistent rate means the new
  owner vmmd keeps dying mid-handoff (operators must inspect
  `events kind='migration_reconciled'` for the failing node).

## Known limits

- `MigratingWatchdogTickLimit=50` — a fleet with >50 simultaneous
  wedged migrations waits for the next tick. Operators should
  raise the cap if the watchdog is the bottleneck (the env var
  is per-instance; raising it is cheap).
- `MigratingWatchdogIntervalSeconds=1` — the watchdog is bounded
  by the 1 s cadence. A lease expiry that races the 1 s tick
  resolves within 1-2 s of the new owner going down.
- The watchdog does NOT emit `app_changed` / `compute_node_changed`
  — the recovery is invisible to the gateway listener. The
  customer's next wake drives a fresh `app_changed` flow.

## Migration slot

**Zero.** No new migrations required. The A5 lineage columns
(00103) carry everything the watchdog reads. The
`instances_state_check` widener (00103) already accepts
`migrating` as a transient state. **No slot reservation fence
needed** — this is the first feature on this branch with zero
DDL.

## Critical files

- `pkg/api/limits.go` — `MigratingWatchdogTickLimit`,
  `MigratingWatchdogIntervalSeconds` (constant block).
- `pkg/wire/metrics.go` — `migratingReconcileDecisions` CounterVec
  + `MigratingReconcileDecisions(outcome)` accessor (parallel to
  the A5 `liveMigrationDecisions`).
- `pkg/state/store.go` — `ListExpiredMigrations`,
  `ReinviteMigratingInstance`, `AbortMigratingInstance` interface
  methods.
- `pkg/state/pgstore.go` — implementations (conditional UPDATEs
  with `state='migrating' AND lease_token=$1` predicate).
- `pkg/state/memstore.go` — mirror (for the in-memory tests).
- `pkg/sched/engine.go` — `ReconcileExpiredMigrations` method,
  `WithMigratingWatchdogTickLimit`, `WithMigratingWatchdogIntervalSeconds`.
- `pkg/sched/migrating_watchdog.go` (new) — the ticker-driven
  loop.
- `pkg/sched/migrating_watchdog_test.go` (new) — table-driven
  tests for the watchdog's per-tick dispatch.
- `cmd/schedd/main.go` — env reads (`FAAS_MIGRATING_WATCHDOG_TICK_LIMIT`,
  `FAAS_MIGRATING_WATCHDOG_INTERVAL_SECONDS`) + ticker wiring.
- `docs/runbooks/gate-a.md` — Live-instance migration paragraph.
- `docs/adr/README.md` — index row.

## Tests

- `pkg/sched/migrating_watchdog_test.go`:
  - `TestMigratingWatchdog_TickDispatches` — ticker fires →
    `Engine.ReconcileExpiredMigrations` called once.
  - `TestMigratingWatchdog_RespectsCadence` — 3 ticks at 1 s
    interval → 3 calls.
  - `TestMigratingWatchdog_HandlesCtxCancel` — ctx cancel →
    ticker stops, Run returns.
- `pkg/sched/engine_test.go` (extended):
  - `TestReconcileExpiredMigrations_ActiveOwnerReinvited` —
    row with `node_id=active`, lease_token stamped → reconciled
    with `outcome="reinvited"`, audit row written.
  - `TestReconcileExpiredMigrations_DeadOwnerParked` — row
    with `node_id=inactive` → reconciled with
    `outcome="hard_deleted"`, row restored to `state='parked'`,
    `node_id=migrated_from_node_id`.
  - `TestReconcileExpiredMigrations_PeerRaceConflict` — peer
    already committed while watchdog was thinking → bumped
    `outcome="conflict"`, no audit row.
  - `TestReconcileExpiredMigrations_RespectsPerTickCap` — 60
    rows, cap=50 → 50 processed, 10 deferred.
- `pkg/state/pgstore_migration_test.go` (extended):
  - `TestPgStore_ReinviteMigratingInstance_Happy` — row in
    `migrating` → UPDATE → `state='running'`, `migrated_at`
    stamped, `lease_token` cleared.
  - `TestPgStore_ReinviteMigratingInstance_LeaseMismatch` —
    wrong lease → `RowsAffected()==0` → `ErrConflict`.
  - `TestPgStore_AbortMigratingInstance_Happy` — row in
    `migrating` → UPDATE → `state='parked'`,
    `node_id=migrated_from_node_id`, `lease_token` cleared.
  - `TestPgStore_AbortMigratingInstance_LeaseMismatch` —
    `ErrConflict`.
- `pkg/state/memstore_test.go` (extended) — mirror for the
  in-memory implementations.

## Verification

- `make test` — full unit suite (the watchdog is unit-only).
- `make test-metal` — exercise the schedd ↔ vmmd path on
  Lima / reference control-plane node.
- `make leakcheck` — zero leaked netns/TAPs/cgroups.
- `make lint` — `go tool golangci-lint run`; the new file
  must pass `gofmt` (the repo-wide gate, per ci.yml).
- `make spec-check` — vacuum + AST parity + git clean.
- `make proto-check` — expected: no proto change; passes
  unchanged.

End-to-end manual smoke (Lima or a reference control-plane node):
1. `make bootstrap && make run` with one schedd, one app,
   wake once.
2. Suspend the new-owner vmmd (kill -STOP the firecracker
   process for the instance) BEFORE the phase-3 commit.
3. Wait 1 s for the watchdog tick.
4. `select id, state, node_id, migrated_at, lease_token from
   instances where id = '<instance_id>';` — row should be
   `state='parked'`, `node_id = <original_owner>`,
   `lease_token IS NULL`.
5. `select kind, payload from events where kind = 'migration_reconciled'
   order by id desc limit 1;` — should show the audit row.
6. `curl -s http://localhost:9100/metrics | grep
   schedd_migrating_reconcile_total` — should show
   `outcome="hard_deleted"` n+1.

## Open follow-ups (deliberately deferred)

- Emit `NotifyInstanceChanged` on watchdog reconciliation for
  gateway listener cache-coherence. Out of scope — the
  customer's next wake drives a fresh flow.
- Cross-node migration of `migrating` rows (the watchdog
  currently hard-deletes; a future PR could re-invite a peer
  vmmd to take over the in-flight handoff). The lease-token
  predicate is the seam.

## Rejected alternatives

- **Notifier on the new owner.** The new owner dying is the
  very failure mode the watchdog covers; it cannot emit a notify
  because it is gone. The ticker sweep is the only reliable
  trigger.
- **Heartbeat-driven watchdog.** The §6.1 watchdog already
  kills stuck instances at 1 s cadence; the migration watchdog
  is a parallel loop with the same cadence. A single combined
  watchdog would conflate two distinct failure modes (state
  stuck vs. migration wedged) and require the same merged
  outcome taxonomy, which is misleading on the §12 dashboard.
- **Per-instance lease expiry timer.** A goroutine per
  wedged migration would scale poorly under a fleet-wide
  bad-owner event (1000+ wedged rows = 1000+ goroutines). The
  ticker sweep is bounded by `MigratingWatchdogTickLimit`.
- **Add a new `migrating_to_node_id` column.** The active-owner
  decision is "is the row's CURRENT `node_id` (the dying vmmd)
  active?" — the new owner is invisible to the DB until Phase 3
  commits. A `migrating_to_node_id` column would let the
  watchdog check the new owner's activity, but it's also dead
  data when Phase 3 commits naturally (the row's `node_id`
  flips to the new owner and the `migrating_to_node_id` is
  redundant). The current path is simpler and the lease-token
  predicate is the seam.
