# Migration source retirement

[ADR-1530](../adr/1530-durable-migration-source-retirement.md) keeps physical
source ownership separate from routing ownership. A successful migration can
serve on its destination while its source remains draining until original VM
cleanup is proven.

| Phase | Meaning and recovery |
| --- | --- |
| bound | The source lease, node, wake and native physical generation are bound before capture. The original live row represents its resources; any missing charge is retained by the source record. |
| retiring | Ownership moved atomically or the original logical owner ended. Its source RAM/CPU/vCPU stays charged independently of the destination row. |
| retired | Original native revocation, exit and resource removal are proven. The physical charge is released; source snapshot cleanup and lease deletion can still be pending. |
| resumed | Destination dispatch was fenced before its still-running or draining source resumed. Captured resource floors remain on that wake. Parked rollbacks retire their original VM instead. |

Pending sources and source leases block maintenance, retirement and node deletion.
An expired deadline, an empty live map or a row pointing to the destination does
not prove source cleanup. Ordinary and incoming admission include retained source
resources, as does protected fleet capacity. A matching live source is counted
once. A source wake returning to the same host does not erase an older retiring
physical generation.

## Coordinated upgrade

1. Pause maintenance submissions and stop old schedd migration/recovery
   dispatchers and vmmd migration receivers. Settle historical handoffs through
   their existing owners and inspect source resources before proceeding. Missing
   source bindings cannot be backfilled from instance-name absence.
2. Apply prerequisites through ADR-1529, then
   `20261017140000004_migration_source_retirement.sql`.
3. Upgrade schedulers and receivers together. Registered source receivers require
   durable native ownership before reserving a new migration. Destination
   reservations require the matching bound source. Keep native recovery enabled
   and configure each vmmd's registered compute-node identity.
4. Preserve the native journal and its private `migration-sources` and
   `migration-admissions` directories, including file ownership and permissions.
   Resume dispatch after dedicated x86_64 KVM migration, daemon/process-death and
   leak qualification passes. Pending records and retained receipts prevent
   downgrade.

Mixed old/new source protocols are not qualified. Unregistered tracker-only
fixtures do not establish production support. No deployment has been performed
from the portable development environment.

## Inspection and recovery

The following queries are read-only:

```sql
SELECT source_node_id, instance_id, source_wake_id, native_generation,
       phase, ram_mb, cpu_millicores, vcpu, proof
FROM migration_sources
ORDER BY source_node_id, instance_id, lease_token;

SELECT n.id, n.name, n.lifecycle,
       (SELECT count(*) FROM migration_sources s
        WHERE s.source_node_id=n.id AND s.phase IN ('bound','retiring'))
         AS pending_source_retirements,
       (SELECT count(*) FROM migration_leases l WHERE l.source_node_id=n.id::text)
         AS source_leases
FROM compute_nodes n
WHERE n.lifecycle IN ('draining','recovering','maintenance');
```

Source vmmd reconciles retiring records at startup and on its lease sweep,
without waiting for lease expiry. Each source pass reads at most 128 records
under a bounded time budget, advancing past failures. Bound records become
eligible on lease expiry or a resume fence. Terminal receipts with snapshot cleanup
pending are also retried immediately; no full unbounded lease scan is used by
registered source receivers. Temporary capture keys are persisted before capture;
snapshot cleanup and durable lease deletion follow the physical receipt. An uncertain
database binding commit retains recovery authority without repeating capture. If
no retained binding exists, capture was never authorized: prepare can release only
that unstarted lease, with the source FK serializing a late binding commit. Lease tokens are bounded to 128 bytes; cleanup proofs to 1 KiB.

Restore database, source vmmd and canonical storage connectivity, then let the
existing receiver sweep or retry the lease-bound acknowledgement. A retained
local physical receipt repairs a lost database acknowledgement without touching a
replacement VM. Once the database has the receipt, retries do not require another
native operation. Late cancellation cannot resume a retiring source. A serving source can resume
only while its original node and wake remain running or draining. A pending source whose owner is parked or ended requires a durable retirement
phase and original-generation cleanup. Completed resumptions return physical
ownership to the ordinary instance lifecycle. Delayed migrating-state publication
cannot cross the source resume fence. The destination cannot dispatch after the source cancellation fence.

If cleanup fails, repair the reported native cleanup error and retain the source
record and lease. If only remote snapshot deletion fails, the physical charge may
already be released while the lease continues to block maintenance. Missing or
corrupt bound journals, changed physical identity, or lost host-lifetime evidence
require investigation on the original native host; they do not authorize manual
row deletion, generic `Destroy(instanceID)`, or forced maintenance. Whole-host
reboot qualification and recovery of destroyed journal evidence are separate
native recovery concerns; an elapsed deadline supplies no substitute proof.

## Validation

Portable tests exercise shared memory/PostgreSQL phase and receipt semantics,
atomic ownership/retirement, resource contention, direct-writer guards, failed
and suppressed writes, deferred commit failures, and drain/resume races. Native
fixtures cover original physical retirement, daemon replacement, cleanup failure,
missing/corrupt evidence, receipt replay, ordinary replacement and delayed
capture. RPC tests cover early/wrong-receiver acknowledgements, lost replies,
restart sweeps and late cancellation.

Portable journal evidence does not establish native KVM or leak acceptance.
Physical migration, real process/daemon death and leak checks must run on the
dedicated supported x86_64 KVM hosts before deployment.
