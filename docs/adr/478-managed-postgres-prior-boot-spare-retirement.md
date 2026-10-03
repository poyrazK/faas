# ADR-478: Retire absent prepared reservations from a prior kernel boot

Date: 2026-10-03

Status: Accepted; native lifecycle acceptance pending, customer cutover disabled

## Context

ADR-477 records prepared networks durably and quarantines them after restart.
Unused reservations can survive a host reboot even though their kernel network
objects cannot. Reconstructing live ownership from journal observations would
still be unsafe for same-boot crashes and guest handoffs.

## Decision

Before installing startup quarantine, vmmd may retire only version-5, unclaimed
network-only spares with complete namespace and primary veth checkpoints from
one kernel boot different from the current boot. Require an empty target and no
guest admission, process or additional assets. Require the namespace and link
creator boot IDs to agree. Incomplete or mixed-boot records stay quarantined.

Inventory surviving guest processes, slot-addressed links, namespace aliases
and jails first. For candidate retirement, additionally inspect all four UID
fields of every current process, including unrelated executables and partially
dropped credentials. Any UID holding the slot, observed slot or source identity,
or present source namespace/jail or ordinary/private host/peer link name retains
the reservation. A dangling namespace marker also retains it. Missing namespace
and jail roots are allowed; unreadable inventory, invalid boot IDs and malformed
process UIDs fail startup before admission.

The prior kernel boot rules out surviving old setup children, held namespaces
and renamed links. Absence alone is insufficient on the same boot. This remains
within vmmd's exclusive physical-resource ownership boundary; concurrent
privileged foreign creation or forged kernel provenance is not authenticated by
these observations.

Under the journal lock, recheck the exact reservation and eligibility, unlink
its stable source record and fsync the journal directory before releasing any
capacity. A failed retirement keeps the in-memory reservation and fails startup;
a confirmed retry may finish removal. Reconcile the remaining journal and only
then install quarantine. Expose the retired count as `reclaimed_prepared_records`
in the existing startup log and recovery report.

This path deletes no physical resources, adopts no recovered guests, returns no
recovered spare to the ready pool and writes no scheduler lifecycle or ledger
state. It requires no record version, schema, RPC, quota, dependency or deployment
environment changes.

## Boundaries

Same-boot crashes, pending transfers, adopted guests, partial creation records,
legacy records and all other resource classes remain quarantined. Automatic
physical restart cleanup, serving recovery and durable all-node drain proof
remain pending. This is reservation retirement after a kernel reboot, not a
customer drain receipt. Filesystem power-loss and supported native x86_64 KVM
acceptance remain pending; customer cutover stays disabled.

## Validation

Portable regressions cover fsync-before-allocation, reopened retirement/replay,
same-boot and incomplete provenance, pending/adopted handoffs, every reserved
network name, dangling markers, jails, unrelated/partial UID holders, malformed
inventory, cancellation and failed-fsync retry without recovered ownership.

Metal diagnostics exercise real foreign nsfs namespaces, veth/dummy links and
an unrelated process running under a reserved jail UID. Injected prior-boot
provenance tests retirement when resources are absent and retention/preservation
on collision. Existing SIGKILL checkpoints verify same-boot crash quarantine.
The dedicated internal GCP node uses nested virtualization; these tests do not
simulate a real host reboot or qualify native/power-loss behavior. Results are
recorded in the [reclamation evidence](../ops/evidence/20261003-managed-postgres-restart-reclaim/README.md).

Final nested diagnostics passed 79 selected top-level tests (255 including
subtests), all five prior-boot cases and the existing five SIGKILL checkpoints,
full Linux/macOS race suites and three leak checks. Changed-code lint, egress
and generated deployment checks passed. Native acceptance remains pending.
