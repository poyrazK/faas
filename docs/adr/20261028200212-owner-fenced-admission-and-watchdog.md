# ADR 20261028200212 · Owner-fenced admission and an owner-scoped watchdog

- **Status:** proposed
- **Date:** 2026-10-28
- **Amends:** ADR-087 (pressure rebalance move), the §6.1 watchdog (ADR-640,
  ADR-470)
- **Related:** ADR-062 (owner shards), ADR-193 (durable node reservation),
  ADR-1741 (one-statement admission), §6.2 invariant 1

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

Per-app max_concurrency (§6.2-1) is enforced only by the owner scheduler's
ledger. That holds while one scheduler admits an app's instances at a time,
and two code paths broke it.

1. **Rebalance vs. an in-flight wake.** A wake checks ownership with a plain
   read of `apps.node_id`, then admits. The pressure rebalancer checked for
   resident instances with a plain read, then moved the app with an
   `UPDATE` that touches neither instances nor any lock admissions take. A
   wake that passed its check could land its instance after the move. The new
   owner's ledger then lacked it, and its scale-out admitted max_concurrency
   more. H9-101 (3 running against max_concurrency 2) was this race with a
   wider window; its fix, the resident pre-check, narrowed the window but did
   not close it.
2. **The watchdog across owners.** Every scheduler runs the stuck-row sweep
   over the whole fleet every second. `KillStuck` claims only WAKING rows
   before tearing down. For COLD_BOOTING and SNAPSHOTTING it relies on
   `appMu`, which is process-local. N schedulers each destroyed the VM,
   emitted the stall event and wrote the transition, and a peer could tear
   down a cold boot the owner was about to publish. The sweep also ran
   inline on the main loop, with up to 5 s per destroy. A partitioned node's
   stuck rows, retried every tick, stalled notifications, crons and heartbeats
   on every scheduler.

## Decision

1. **Owner fence on admission.** A wake, scale-out or restore insert is bound
   with `state.WithAppOwnerFence(ctx, ownerNodeID)`. In the same transaction
   as the insert, the store reads `apps.node_id` `FOR KEY SHARE` and refuses
   with `ErrAppOwnerMoved` when another node owns the app. An app with no
   node is every scheduler's, as `ownsApp` decides.
   - On the one-statement path this is `admission_check_app_owner`, called by
     a new 21-argument `admit_wake_instance` (migration
     `20261028200212696`).
   - The transaction path adds it to `reserveNodeDB`'s existing statement.
   - The statement-by-statement fallback runs it as its own query.

   No round trip is added to a wake. `FOR KEY SHARE` is the lock the insert's
   `instances_app_id_fkey` check already takes, so ordinary app updates, which
   take `FOR NO KEY UPDATE`, never wait on admissions. The scheduler reports
   the refusal as `ErrAppOwnedByAnotherNode`. Primes and warm pools are not
   fenced.
2. **Parked-only move.** The pressure rebalancer moves an app through
   `ReassignParkedAppOwner`, which:
   - takes `FOR UPDATE` on the app row, waiting for every in-flight
     admission's key-share lock;
   - checks for resident (`CountsForRAM`) instances in a fresh statement;
   - refuses with `ErrAppResident`, and otherwise moves the app.

   Either order is safe. A move that locks first commits before the fence
   reads, and the admission refuses. An admission that locks first commits
   before the move's check, and the move refuses. The resident pre-check
   remains as a cheap early exit. Generic `ReassignAppOwner` (ownership
   recovery, operators) is unchanged.
3. **Owner-scoped watchdog.** Each scheduler acts only on stuck rows of apps
   it owns. A row with no app, or whose app cannot be read, is swept as
   before. The owner's `appMu` again serializes a kill with the owner's own
   wake and park, which is the assumption `KillStuck` was written under.
   Rows of a dead owner's apps are swept once ownership recovery moves the
   apps.
4. **Watchdog off the loop.** The sweep runs on the loop's work pool (one
   slot; a tick during a sweep coalesces), as the reaper does.

Unchanged: the claim-less COLD_BOOTING and SNAPSHOTTING kill within one
owner (ADR-470 keeps RAM booked until teardown is confirmed), and the 30 s
cold-boot sweep budget.

## Rollout

The previous `admit_wake_instance` signature delegates with no owner, so the
previous release admits unfenced, as before. A scheduler that starts before
the migration runs finds no new functions and uses the statement path, which
applies the fence. The parked-only move needs no migration.

## Validation

- `checkAppOwnerFence` runs on MemStore and on PostgreSQL, through the
  function and the statement paths. It covers unplaced apps, the owner's
  admission, another node's refusal, refusing a move while instances are
  resident, the move once they are gone, and the previous owner's refusal
  after the move.
- `TestAppOwnerFencePostgresInterleavings` holds each side's lock open in a
  raw transaction and checks that the other side waits and then refuses.
- `TestWatchdogSweepsOnlyOwnedApps` and
  `TestPressureRebalanceKeepsAnAppWithResidentInstances` (sched).
- `TestPropertyOwnersAdmittingTogetherKeepNodeBudgets` moves parked apps
  between three owner schedulers while they scale and park, and checks §6.2-1
  alongside the node budgets.
