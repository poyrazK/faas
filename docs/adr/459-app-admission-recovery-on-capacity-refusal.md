# ADR-459 · Recover terminal app reservations before a capacity refusal

- **Status:** proposed
- **Date:** 2026-10-03
- **Amends:** ADR-028 and spec §6.2

The scheduler already reconciles an app's local admission reservations with
durable instance states when the app would hit its concurrency cap. A peer's
completed teardown can nevertheless leave local RAM, vCPU or CPU accounting
above reality while concurrency remains below the cap. Placement then refuses
the next wake without reaching the existing recovery trigger.

When wake placement or ledger admission returns `CodeCapacity`, reconcile the
requesting app under its existing app lock and retry the rejected decision once
if terminal reservations were released. Apply this before a VM RPC, for cold boot and snapshot
restore. Healthy admission adds no query. A reconciliation read failure preserves
the original capacity refusal and releases nothing. Retry uses the same placement,
restore-pressure and admission checks; it does not increase any resource ceiling.

Only PARKED, STOPPED and FAILED durable rows returned for this app can justify
release. Missing rows retain their reservations because an operation may still be inserting or recovering an
instance. WAKING, COLD_BOOTING, RUNNING, DRAINING, SNAPSHOTTING, MIGRATING and WARM
retain resident capacity. Reconciliation does not inspect or destroy a VM, change
app ownership, or reset the node ledger. Stale reservations belonging to other
apps, deleted rows and previous owners require separate evidence and recovery.

The account-deletion terminal state is stamped before VM destruction returns.
Keep its reservation until the deletion reconciler successfully destroys the
VM; do not interpret that terminal state as proof that physical capacity is free.

Validate that below-cap terminal rows no longer strand RAM/vCPU/CPU capacity,
and that resident rows, missing rows, read failures and genuine node saturation
still refuse admission. Snapshot restore pressure must be released normally.
Native VM lifecycle acceptance remains a rollout gate; local fake-VMM tests
establish admission behavior, not production latency improvement.
