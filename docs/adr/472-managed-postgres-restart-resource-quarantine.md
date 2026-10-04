# ADR-472: Quarantine resources surviving a vmmd restart

Date: 2026-10-02

Status: Accepted; supported native acceptance pending, customer cutover disabled

## Context

ADR-471 preserves failure reports across process exit, but refuses replay when a
replacement Manager cannot prove guest ownership. Firecracker can survive vmmd;
the durable-state startup sweep intentionally preserves scheduler-live guests.
Their allocator slots are lost with the old Manager. A fresh Wake or prepared
network could therefore reuse the surviving guest's jail UID, veth, host IP or
vsock CID (spec §6.2-5). An ordinary stop of an unknown Manager ID can also return
success even though that guest survives (ADR-469/396).

## Decision

Before starting a prepared-network pool or accepting RPCs, Linux vmmd inventories
`/proc`, `/sys/class/net`, `/run/netns` and the current version's jail root. It
reserves slots observed in Firecracker kernel UIDs, pre-exec jailer destination
UIDs, public veth names and private side-link names. Firecracker's post-exec argv
does not retain jailer's `--uid`; read all four kernel UID fields instead. Reject
inconsistent, missing or out-of-range identities. Reserve every observed slot,
including multiple processes with the same instance ID.

Record instance IDs from guest processes, named namespaces and jails, including
`build-` prefixes and compact internal IDs. Require the path-safe alphanumeric,
underscore and hyphen alphabet; this observation does not use the UUID-only
destructive reaper gate. Unknown IDs on matched guest processes fail startup.
Reject their Wake, job boot, resumable operation and teardown requests with
`ErrRestartQuarantine`. A fresh Manager cannot acknowledge a successful stop of
one of these instances. Quarantine never populates the live map or retained
cleanup identities, and never satisfies `HasInstanceOwnership`: recovered
failure reports remain pending under ADR-471.

Exclude reserved slots from both ordinary and prepared-network allocation.
Do not fabricate leases or make these slots releasable through `Release`.
Inventory is startup-only and atomic with respect to Manager operation admission.
An unreadable required root, process argv or matched guest UID fails startup;
missing namespace/jail roots on an unused node are allowed. Processes disappearing
during enumeration are skipped. No age or database-state test authorizes reuse.
The inventory itself never signals a process or removes a resource. The existing
durable-state sweeps keep their separate behavior.

## Recovery boundary

Quarantine lasts for this Manager's lifetime. Even a subsequently reaped orphan's
slot stays reserved until a fresh startup observes its absence. This deliberately
trades some allocation capacity for avoiding an unproven release. Logs record
the slot, instance and process counts and whether reconciliation is required.
These counts are observations, not reconstructed scheduler RAM/concurrency.

This is the admission prerequisite for recovery, not guest reattachment. It
does not reconstruct routing, process watchdogs, cgroup/workload ownership,
artifact mounts or durable cleanup receipts. It does not resume report replay,
provide an operator quarantine-clear API, or establish drain proof. Remaining
work is a durable vmmd resource journal with process-incarnation verification,
confirmed cleanup and scheduler reconciliation. Old binaries and lost spool
storage retain their existing limitations. Customer cutover stays disabled.

Portable regressions cover kernel UID recovery, duplicate process observations,
partial network resources, prepared allocation, rejected boot/stop/resume,
unknown report ownership, compact/prefixed process-only identities, ambiguous
identity and failed/late inventory.
The metal regression boots a real guest, constructs a fresh Manager/VMM,
inventories its resources, refuses an unowned stop and boots a second guest with
distinct slot/UID/IP/CID for UUID, builder-prefixed and compact IDs. The original
Manager is retained solely for fixture cleanup: this does not simulate a full
daemon crash or prove recovered routing.
Native x86_64 Linux KVM and leakcheck remain the release acceptance requirement.

Internal nested-node [diagnostics](../ops/evidence/20261002-managed-postgres-restart-quarantine/README.md)
passed 13 selected top-level metal tests (29 including subtests), including all
three real-guest ID forms, and three in-run leak checks. Complete portable race
checks and changed-code lint also passed. This evidence does not satisfy
supported native lifecycle acceptance.
