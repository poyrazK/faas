# ADR-031 · Durable build queue via in-process SKIP-LOCKED worker inside builderd

- **Status:** accepted
- **Date:** 2026-07-23
- **Decision:** Move ownership of the build queue from `pg_notify` alone
  to a two-layer delivery model:

  1. **Fast path (unchanged).** `apid` emits
     `pg_notify('build_queued', {build, deployment, app, kind})` immediately
     after `CreateBuild` (PR-A). `cmd/builderd/main.go` subscribes via
     `db.SubscribeWithReconnect` and dispatches each notification to
     `b.ProcessOne(ctx, buildID)`. Round-trip on the EX44 is ≈ 200 ms.

  2. **Durability net (new).** `cmd/builderd/main.go` runs a
     `workerLoop` goroutine that ticks every
     `FAAS_BUILDER_POLL_INTERVAL` (default 2 s) and calls
     `b.ProcessNext(ctx)`, which under the hood does
     `state.ClaimNextQueuedBuild` — a single statement
     `update builds set status='running', started_at=now()
      where id = (select id from builds where status='queued'
                  order by enqueued_at asc limit 1 for update skip locked)
      returning …`. Empty queue returns `state.ErrNotFound` (logged at
     debug; the worker treats this as the expected idle state). On a
     slot denial (`DecideSlot → !Allowed`, future in-process slot
     budget) the row is `RequeueBuild`-ed (status='queued',
     `started_at=NULL`, `enqueued_at` untouched) so FIFO survives a
     wake-surge. The notification-driven and the poll-driven surfaces
     share `processClaimedBuild` 1:1 — the only divergence is the claim
     SQL.

  PR-A's `imaged.runBuildReapTick` is **removed** entirely; the
  durability gap it patched (a single missed `pg_notify`) is closed
  by the new worker that scans the queue directly. imaged no longer
  subscribes to `build_queued` and no longer touches `builds.status`
  — its build-related surface area shrinks to the snapshot pipeline
  that consumes `db.NotifySnapshotBoot` (`pkg/imaged/handler.go`).

- **Why:** PR-A shipped a 30 s reaper inside `imaged` as a rescue for
  the case "apid emits `build_queued` but builderd misses the notify
  (Postgres restart between `NOTIFY` and `LISTEN`, apid crashes
  between `CreateBuild` and `Notify`, transient socket error on the
  builderd side)". The reaper closes the soft gap but **not** the hard
  ones:

  - apid dies after `CreateBuild` returns to the HTTP handler but
    before `db.Notify` runs (a SIGKILL on the request goroutine, an
    OOM, a deploy of apid mid-handler). The `builds` row is `queued`
    forever; no notification will ever land.
  - Postgres restarts mid-deploy. `LISTEN` sockets drop, the
    SubscribeWithReconnect wrapper reconnects, but the
    `pg_notify('build_queued', …)` that fired during the outage is
    lost. Postgres `NOTIFY` is **not** durable across a server restart.
  - A build sits in queued for > 30 s (controller restart window) —
    the reaper catches it but only after a full minute of latency on
    a hot deploy.

  Both gaps are closed by *the worker polling the queue directly*.
  Postgres is the queue's source of truth; `SKIP LOCKED` makes the
  worker + the LISTEN path race-safe (both surfaces compete for the
  same row with `ClaimQueuedBuild` / `ClaimNextQueuedBuild`; the
  loser sees `ErrNotFound` and sleeps). PR-A's reaper is no longer
  load-bearing — its deletion is a one-line plus the channel-list
  cleanup.

- **Consequences:**

  - **`pkg/state/store.go`** grows two interface methods:
    `ClaimNextQueuedBuild(ctx) (Build, error)` and
    `RequeueBuild(ctx, id string) error`. The PgStore
    implementation uses `FOR UPDATE SKIP LOCKED` + an `UPDATE …
    RETURNING` so the CAS is one round-trip; the MemStore mirrors
    under `m.mu` for the unit tests.
  - **`pkg/builderd.Builderd`** splits `ProcessOne` into
    `ProcessOne` (LISTEN surface; takes a buildID, CAS-claims via
    `ClaimQueuedBuild`) and `ProcessNext` (worker surface; picks the
    next queued row via `ClaimNextQueuedBuild`). Both call a private
    `processClaimedBuild` so the pipeline (cache check → slot →
    spawn → wait → terminal) is shared. A new exported sentinel
    `ErrNoSlot` covers the denial path.
  - **No-slot requeue** lives inside `processClaimedBuild` (not in
    the worker) so the LISTEN surface and the poll surface both
    preserve FIFO on denial. `markFailed` is no longer called on
    no-slot — the row stays `queued` and `started_at=NULL`. This
    fixes the pre-PR-B behavior where a `DecideSlot → !Allowed`
    flipped the deployment to `failed` even though the build was
    never attempted.
  - **A test-only `WithSlotDecider` hook** on `Builderd` lets unit
    tests inject a deny-all decider without standing up a full
    `ResidencyProbe`. Production wiring stays untouched; the hook
    is gated by an explicit test call.
  - **`cmd/builderd/main.go`** launches the worker as a goroutine
    next to the `SubscribeWithReconnect` listener. The worker logs
    at `Debug` on empty queue (the expected idle state) and `Warn`
    on unknown errors; the LISTEN path stays `Warn` on every error.
    `cfg.PollInterval` (default 2 s, env tunable via
    `FAAS_BUILDER_POLL_INTERVAL`) controls cadence.
  - **imaged cleanup.** `pkg/imaged/loop.go` drops
    `runBuildReapTick`, the `reapCh` field, the
    `WithBuildReapChannel` setter, the `ReapBuildEvery` /
    `BuildReapThreshold` `LoopConfig` fields, and the `case
    db.NotifyBuildQueued:` arm in `Run`. The `build_queued`
    channel is removed from imaged's `SubscribeWithReconnect`
    channel list. `cmd/imaged/main.go` drops the
    `FAAS_REAP_*` env wiring. The single image-deploy build-claim
    seam is unaffected (imaged still subscribes to
    `snapshot_boot`).

- **Rejected alternatives:**

  - **`pg_cron` extension + `cron.schedule('claim-build-worker',
    'SELECT … FROM claim_next_queued_build()', '* * * * *')`.**
    Closed-form durable, no Go worker, but pg_cron is **not
    installed** on the EX44 Postgres (no migration, no
    `shared_preload_libraries`, no ansible role entry). Adopting it
    would mean a `postgresql-contrib` apt-install + a Postgres
    `restart` (which in turn requires a tenant-`paused` maintenance
    window per the §13 ops playbook) + a new migration that loads
    the extension. The operator-cost / durability benefit ratio
    doesn't justify it for a beta one-box. The SQL shape (`SELECT
    … FOR UPDATE SKIP LOCKED`) is identical to what the new
    in-process worker uses, so a future migration to pg_cron is a
    straight swap.

  - **Dedicated `cmd/buildqueued` daemon.** More process boundary,
    more systemd unit, more wire surface. The worker is 12 lines of
    Go inside builderd — extracting it to its own daemon adds
    deployment cost (a new `faas-buildqueued.service`, a new
    `/metrics` scrape target, a new gRPC auth ticket) for zero
    isolation benefit. The 1 + 1 opportunistic builder slot is
    enforced inside `ProcessOne` / `processClaimedBuild`; the
    worker needs to share the same process for that enforcement to
    be in-tx.

  - **Keep PR-A's `imaged` reaper as a belt-and-suspenders backup
    layer.** Two sources of truth for queue ownership
    (`imaged`'s reaper + the new worker) is worse than one: both
    surfaces would call `ClaimQueuedBuild` (the CAS makes the
    second caller a no-op) but the reaper's `30 s` cadence would
    still dominate the design conversation. Single owner is the
    better story.

  - **Always-on worker without LISTEN.** Drops the fast path, costs
    ≈ 2 s of build latency on every deploy for no durability
    benefit — Postgres `NOTIFY` round-trip on the EX44 is ≈ 200 ms.
    The two-layer model keeps the warm path warm and the cold path
    durable.

  - **`LISTEN`-only, no worker.** The status quo before PR-A.
    Postgres restarts, apid SIGKILL mid-deploy, controller OOMs all
    leak rows to `queued` forever. Acceptable pre-beta, ship-blocker
    for the beta cohort.

- **Out-of-scope follow-ups (deferred to PR-C or later):**

  - **Stuck-running build recovery.** A row in `status='running'`
    whose builderd died is not auto-requeued today. The spec §4.5
    build timeout is 10 min; a stall longer than that is
    operator-visible only. The fix is a one-line SQL sweep
    (`update builds set status='queued', started_at=NULL where
    status='running' and started_at < now() - interval '15
    minutes'`) added either to the existing worker tick or to
    schedd's watchdog (`pkg/sched/watchdog.go::sweepRuns` is the
    structural template).
  - **Multi-builderd horizontal scale.** `SKIP LOCKED` makes
    concurrent pollers safe at the SQL layer, but the
    `1 + 1 opportunistic` slot budget is per-process. Two
    `faas-builderd` processes on the EX44 would exceed the budget.
    Not a one-box concern; revisit if the cluster grows past
    one control plane.
  - **Promotion to pg_cron.** If the EX44 ever grows pg_cron, the
    in-process worker can be deleted and the `SELECT` promoted to a
    `cron.schedule` call. The CAS SQL is the same in both places.

## State machine (final, ADR-031)

```
                                ┌──────────────────┐
                                │   BuildQueued    │◀────────────────┐
                                └────────┬─────────┘                 │
                                         │ ClaimQueuedBuild          │ RequeueBuild
                                         │ ClaimNextQueuedBuild      │ (no-slot, FIFO
                                         │   (CAS, SKIP LOCKED)      │   preserved)
                                         ▼                            │
                                ┌──────────────────┐                  │
                                │   BuildRunning   │──────────────────┘
                                └────────┬─────────┘
                                         │
                ┌────────────────────────┼────────────────────────┐
                ▼                        ▼                        ▼
        ┌──────────────┐         ┌──────────────┐         ┌──────────────┐
        │ BuildSucceeded│         │ BuildFailed   │         │ ErrNoSlot    │
        └──────────────┘         └──────────────┘         │ → Requeued   │
                                                          └──────────────┘
```

## Cross-reference

- `pkg/state/store.go` — `ClaimNextQueuedBuild`, `RequeueBuild`.
- `pkg/state/pgstore.go` — single-statement CAS with `FOR UPDATE
  SKIP LOCKED`.
- `pkg/state/memstore.go` — mutex-mirrored shape.
- `pkg/builderd/builderd.go` — `ProcessOne` / `ProcessNext` /
  `processClaimedBuild` split; `ErrNoSlot`; `WithSlotDecider` test
  hook; `RequeueBuild` on the no-slot branch.
- `cmd/builderd/main.go` — `workerLoop` goroutine;
  `cfg.PollInterval` (TOML + `FAAS_BUILDER_POLL_INTERVAL` env).
- `pkg/imaged/loop.go`, `pkg/imaged/handler.go`,
  `cmd/imaged/main.go` — reaper deletion.
- `pkg/builderd/builderd_test.go` — `TestProcessNext_*` and
  `TestProcessOne_NoSlotDoesNotMarkFailed` coverage.
