# Incoming migration recovery

[ADR-1529](../adr/1529-durable-inbound-migration-reservations.md) reserves the
destination before adoption. The immutable record binds both nodes and wakes,
destination lifecycle generation, source lease, snapshot keys, resources, request
digest and deadline. Pending records consume destination RAM, CPU and vCPU,
participate in protected fleet capacity, and block drain completion and retirement.
Ownership conversion preserves those bounds in resident accounting until that
wake ends; it cannot silently free a larger RAM or CPU reservation. Source cleanup
after ownership commit additionally follows the [source retirement protocol](migration-source-retirement.md); a committed incoming receipt alone cannot finish source maintenance.

| Phase | Recovery authority |
| --- | --- |
| reserved | Cancel atomically with `no_dispatch`; no VM start was granted. |
| adopting | Dispatch was granted once; its physical outcome may be unknown. |
| adopted | Original response is retained; ownership may still be uncommitted. |
| cleaning | Delayed ownership commit is fenced; original physical cleanup is required. |
| cleaned | Original cleanup proof is retained; the source owner can finish rollback. |
| committed | Ownership and runtime evidence consumed the reservation atomically. |

Expiry makes a record eligible for reconciliation. It does not release capacity.
An unknown adoption is never dispatched again. A lost response is replayed from
the database receipt. Source cancel, lease expiry, lease replacement and the
migration watchdog retain unresolved source ownership until destination cleanup
is proven. Destination recovery scans at most 128 records with a bounded time
budget and advances its cursor past failures. Adoption receipts are limited to
64 KiB and cleanup proofs to 1 KiB.

## Coordinated upgrade

1. Pause maintenance submissions and stop old schedd migration/recovery
   dispatchers and vmmd migration receivers. Settle active historical handoffs
   through their existing owners; missing incoming records are not adoption or
   evacuation proof.
2. Apply prerequisites through ADR-1528, then
   `20261017140000003_inbound_migration_reservations.sql`.
3. Install upgraded dispatchers and receivers together. Enable the existing native
   process recovery configuration and verify each receiver has its registered
   compute-node identity. A production receiver refuses missing admission IDs or
   unavailable native ownership before granting a new dispatch. An unstarted
   reservation can then be cancelled without physical cleanup.
4. Preserve the private incoming and native journals with their ownership and
   permissions. Resume dispatch only after dedicated x86_64 KVM migration,
   process-death, cleanup and leak qualification passes.

No migration receipts are backfilled. Admission history blocks downgrade,
including terminal records.

## Inspection and recovery

```sql
SELECT r.id, r.instance_id, n.name, n.lifecycle, r.destination_generation,
       r.phase, r.expires_at, r.ram_mb, r.cpu_millicores, r.vcpu
FROM migration_admissions r
LEFT JOIN compute_nodes n ON n.id=r.destination_node_id
WHERE r.phase NOT IN ('cleaned','committed')
ORDER BY r.destination_node_id, r.id;
```

Restore database and destination connectivity, then let vmmd's startup/lease
sweep retry the admission-backed retirement RPC. Cleanup first revokes delayed
creation, then checks the original native generation's exit and resource-removal
evidence. A lost database acknowledgement reuses its retained local proof. A
different generation, corrupt/missing bound journal, or unattested recovered
producer retains uncertainty for the existing native recovery owner.

Do not delete rows or journals, change phases manually, or treat a missing live-map
entry or elapsed deadline as cleanup proof. Committed ownership uses the existing
source acknowledgement/commit resolver; a retirement request cannot destroy that
destination. Once cleanup is durable, source lease recovery completes rollback.
If the original scheduler still retains a failed attempt in its process ledger,
retry its commit resolver or restart that scheduler to rebuild the ledger. This
can temporarily overcount capacity; it cannot erase the durable reservation.

Portable tests model native process and resource evidence. They do not qualify
physical migration, host removal or absence of host leaks.
