# Node drain and recovery completion

[ADR-1527](../adr/1527-generation-fenced-node-completion.md) binds completion to
the lifecycle generation captured before a recovery sweep. Lifecycle changes and
changes to drain/recovery initiation timestamps advance that generation. Heartbeats
and registration metadata preserve it. The release/configuration `generation`
column is independent.

Completion checks the captured cycle and current remaining instances, then commits
the lifecycle transition, immutable completion receipt and audit outbox handoff in
one transaction. The capacity-policy lock precedes the node lock and serializes
the final check with instance admissions, moves and state writes, including when
service capacity protection is disabled. An obsolete uncompleted sweep cannot
complete a later cycle. A retry of an already committed completion returns its
original receipt before inspecting the current node.

Drain and force-drain reach non-admitting `maintenance` only when no resident
instances remain. This includes warm and frozen instances, which the existing
recovery enumeration does not include. Such rows keep completion pending; inspect
and evacuate them through their existing workload owners. This change does not
add VM evacuation behavior. Recovery retains its existing eligibility rule:
migrating and snapshotting rows block completion; healthy local running rows and
newly admitted boots can remain as the node returns to `active`.

## Upgrade

1. Stop every old schedd recovery controller and maintenance dispatcher. Pause
   maintenance submissions during the coordinated upgrade. Old unfenced
   completion writes will fail after the migration.
2. Apply prerequisite migrations, including ADR-1526's operator node execution
   migration, then `20261017140000001_node_lifecycle_completion.sql`. Existing
   nodes begin at lifecycle generation 1; this backfill creates no historical
   completion evidence. Follow the [node maintenance guide](operator-node-maintenance.md)
   to reconcile any quarantined legacy node intents.
3. Install and start only upgraded schedulers. They reread current generations
   before sweeping and use transactional completion. Existing pending cycles
   can complete after their current work clears.
4. Check lifecycle, resident work and the receipt/outbox handoff before taking a
   node out of service. Use the existing drain-status command for evacuation
   progress. A successful drain *request* still means the request committed;
   `maintenance` with a completion receipt records completed evacuation.

These read-only queries show the current cycle and its completion history:

```sql
SELECT id, name, lifecycle, lifecycle_generation,
       drain_initiated_at, drain_completed_at, recovery_initiated_at,
       last_recovery_outcome
FROM compute_nodes ORDER BY name;

SELECT node_id, source_generation, result_generation,
       source_lifecycle, result_lifecycle, initiated_at, completed_at,
       drained_count, migrated_count, recreated_count, audit_outbox_id
FROM node_lifecycle_completions ORDER BY completed_at DESC;
```

## Retry and recovery

Only the database completion is retried: up to three attempts, 100 ms apart,
within a ten-second persistence budget. Failure before commit leaves the lifecycle,
receipt and audit handoff unchanged. A later sweep can retry. A lost commit reply
replays the original receipt, even after another cycle starts or the node is
deleted. Neither case repeats a VM operation as part of completion persistence.

The existing audit outbox delivers `node.drained` or `node.recovered` after commit.
Delivery failure leaves a durable handoff for background delivery. Recovery SSE
and counters observe a newly delivered completion without appending another audit;
live fan-out is best effort. Receipt replay does not publish another completion.

Do not reset lifecycle generations, use legacy completion setters, edit receipts,
or delete evidence to force a downgrade. Receipt immutability and the completion
transition guard reject unsafe writes. Audit retention does not remove receipts.
Downgrade refuses while any completion receipt or advanced node generation exists.

Native migration and evacuation qualification remains a separate fleet acceptance
gate. Database and scheduler transaction checks do not establish VM behavior.

## Verification

Targeted memory/PostgreSQL tests pass for metadata and lifecycle generations,
resident/recovery preconditions, obsolete and concurrent controllers, immutable
replay after later cycles, node deletion and outbox retention. PostgreSQL injection
checks failed/suppressed node, receipt and audit writes plus deferred commit
failure; admission serialization, capacity lock ordering with protection enabled
and disabled, direct-writer guards and downgrade preservation also pass.

Scheduler tests cover paused old sweeps, lost commit replies, persistence retries,
deferred audit delivery and warm/frozen rows. Existing recovery, heartbeat and
operator regressions pass, as do events/statetest suites, memory-store and runner
race detection, scoped production/test lint, SQLC compile/diff and restored-schema
parity against fresh migrations. Nothing deployed.
