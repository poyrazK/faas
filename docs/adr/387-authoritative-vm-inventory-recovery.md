# ADR-387: Authoritative VM inventory recovery

- **Status:** accepted
- **Date:** 2026-09-30
- **Milestone:** M9 managed service recovery
- **Related:** ADR-053, ADR-137, ADR-191, ADR-208

## Problem

A healthy-node presence reconciler already repairs missing RUNNING rows, but it
infers a complete inventory from a live count and cgroup metric rows. Failed
collection can resemble an empty node. It also retains confirmation across a
reporting outage and queries app scheduler ownership rather than instance
physical placement. A process exit whose one-shot relay failed can leave a
Manager cleanup entry behind even though Firecracker is gone.

## Decision

Add an optional process inventory to the existing capacity stream. vmmd samples
JailerVMM's process registry, which includes booting and paused processes and
removes exited children when their process waiter completes. A Manager entry
retained for cleanup is not a live process. An unsupported VMM returns unknown.
The publisher starts only after vmmd's orphan/startup cleanup has completed.
Resource metrics and capacity remain independent of this inventory.

The new inventory has an explicit completeness bit and its own ECDSA P-256
signature. The canonical digest binds a versioned domain, length-prefixed node
and key identities, report timestamp, completeness, count and sorted
length-prefixed instance IDs. The existing v1 capacity signature is unchanged,
so older receivers continue to accept capacity frames during rolling upgrades.
Older/unsigned producers cannot provide destructive inventory evidence. The
receiver verifies the inventory against the node-scoped registered key before
passing it to reconciliation; unavailable or invalid inventory resets evidence.

Confirm two distinct, increasing, complete reports within the five-second
telemetry freshness budget. Reports outside that clock-skew/age budget and
reporting interruptions reset confirmation. Identical timestamps never count
as a second observation. Duplicate or empty instance IDs invalidate a report.
An explicit complete empty report is valid evidence, including for the node's
only VM. Cache state is ephemeral: a scheduler restart requires new evidence.

Reconcile physical placement (`instances.node_id`), not app scheduler ownership.
Only RUNNING instances older than the existing startup grace are eligible.
Reread state, placement and the latest inventory under the app lifecycle lock
before cleanup, and retain the app scheduler ownership gate. A physical-node
report never grants a scheduler authority over another scheduler's app.
WAKING, COLD_BOOTING, draining and migration rows remain owned
by their existing controllers. Cleanup goes through vmmd before the conditional
node-owned terminal update. Cleanup/database failures retry on subsequent fresh
reports; they do not release capacity or admit another replica prematurely.
Successful repair stamps terminal time, releases capacity, emits route/audit
notifications and schedules the existing service/worker reconciler. No customer
request is needed to replace a service replica. Repair work retains the existing
per-sweep instance cap and bounded database context.

`FAAS_SCHEDD_NODE_INVENTORY_ENFORCE=1` enables writes. The Ansible control-plane
and compute roles project `faas_node_inventory_reconcile_enforce` (default false)
into a systemd drop-in. This canary switch is separate from ADR-191's older
metric-based diagnostic sweep. Keep `FAAS_SCHEDD_RECONCILE_ENFORCE` unset.

## Qualification and rollout

Portable race tests cover service replacement without traffic, terminal time,
ledger release, routing notifications, startup and transition protection,
telemetry interruptions, repeated/stale reports, signature tampering and retry
after cleanup failure. The native substrate gate
`TestMetalInstanceInventoryAfterProcessKill` SIGKILLs real Firecracker, drops its
exit relay, verifies empty process inventory, then asserts cleanup releases the
Manager entry and allocator lease. Native KVM execution and leakcheck are
required before enabling enforcement for a fleet.

Deploy compatible producers and receivers in report-only mode, compare signed
inventory findings against the host, then enable one canary scheduler. Observe
`schedd_instance_divergence_total{outcome="suppressed"|"failed"}` and customer
request/replica health. Turn the host variable back off to disable repair.
Clock skew beyond five seconds prevents repair and must be corrected by ops;
it is not treated as VM death. This PR does not establish host-loss, database
failover or a platform availability SLA.
