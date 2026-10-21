# ADR-1530: Durable migration source retirement

Date: 2026-10-17
Status: Accepted; implemented, native qualification pending

## Context

ADR-1529 makes incoming migration capacity durable. Ownership commit moves the
instance row away from the source before its best-effort acknowledgement destroys
the paused source VM. A failed acknowledgement leaves a durable source lease,
but that lease does not block node completion or retain source capacity. Cleanup
by instance name can also target a later VM generation.

## Decision

Before pausing a production source, vmmd binds its source lease to the registered
node, source wake, native generation and a digest of the original native physical
lease. An immutable durable source record captures its RAM/CPU/vCPU bounds. Its
phases are bound, retiring, retired and resumed. Ownership movement atomically
changes bound to retiring; it cannot commit after source resume is fenced.
Unresolved sources block maintenance, retirement and node deletion. Their physical
resources remain charged after the instance row moves, including ordinary and
incoming admission and protected fleet capacity. A source instance already
representing that same physical wake is counted once.

Source cleanup uses the exact native generation and physical lease digest. A
private fsynced journal retains receipts by source lease, and shares the incoming
migration preparation lock so delayed snapshots, cleanup and replacement cannot
cross generations. VM absence is not cleanup proof: original native revocation,
exit and resource-removal evidence is required. Receipts replay without touching
a replacement. Ordinary preparation persists original cleanup proof before
replacing its native frame. Missing or corrupt bound evidence keeps the operation
unresolved. Cancellation fences destination dispatch before any physical action.
A running or draining source can resume with a retained receipt and resident
resource floors. A parked rollback or ended owner retires its original VM;
resuming it would violate the zero-resident-RAM meaning of parked. Source resume
also fences a delayed migrating-state publication, preserving the serving owner.

Source vmmd recovers retiring records immediately after restart and on bounded,
fair sweeps; lease expiry is a retry signal and never releases capacity. A failed
physical cleanup or database acknowledgement retains its durable source record.
The existing source acknowledgement RPC only succeeds for committed retirement
or a retained retirement receipt. Registered production receivers require this
protocol; unregistered tracker-only fixtures remain explicit legacy test seams.

## Rollout and recovery

Stop old migration dispatchers and receivers, settle historical handoffs, apply
the migration and upgrade both sides together. Do not invent physical bindings
for historical paused sources. Preserve source, incoming and native journals.
Do not clear rows or journals to force maintenance. Pending records and retained
receipts block downgrade. Native migration, process-death and leak acceptance on
dedicated x86_64 KVM hosts remains a deployment gate.

## Validation

Shared MemStore/PostgreSQL contracts cover atomic ownership/retirement, capacity,
drain races, immutable receipts and source-lease retention. Native portable
fixtures cover original-generation retirement, replacement preservation, lost
replies, daemon replacement and delayed snapshot fencing. RPC and scheduler
regressions, targeted races, SQLC/schema checks and scoped lint accompany rollout
and recovery instructions. Portable evidence is reported separately from native
acceptance.

Portable validation passes: shared memory/PostgreSQL contracts, physical source
capacity and protected fleet accounting, drain/source-marker races, rollback,
raw-writer/immutable-handle guards and suppressed/deferred transaction failures.
Full scheduler and RPC regressions, portable native fixtures, targeted races,
SQLC generation, schema restore and downgrade/reapply parity, scoped lint and
both daemon link checks provide development evidence. No KVM device is available;
physical migration, real process/daemon death and leak qualification remain
pending on the supported native hosts. Nothing deployed.
