# ADR-1529: Durable inbound migration reservations

Date: 2026-10-17
Status: Accepted; implemented, native qualification pending

## Context

ADR-1528 fences ownership commit against a changed destination generation. The
destination VM is restored before that commit, while its instance row still names
the source. Its local ledger reservation neither survives schedd restart nor
participates in drain completion. Cleanup after a refused or uncertain adoption
can fail. Source lease expiry must not resume the source while a destination may
still run the same workload.

## Decision

1. Before destination adoption, schedd persists an immutable admission containing
   source/destination node and wake identities, destination lifecycle generation,
   source lease, snapshot keys, resource bounds, request digest and deadline.
   Reservation and ordinary admission share capacity-policy, node and reservation
   lock order. Unresolved reservations count against destination RAM/CPU/vCPU and
   protected fleet capacity, and block lifecycle completion and retirement.
2. Admission phases are reserved, adopting, adopted, cleaning, cleaned and
   committed. Expiry permits reconciliation, never evidence release. An atomic
   adopting claim grants one dispatch. Replays return its retained acknowledgement
   or an unresolved result; they never repeat an unknown VM start. Immutable
   receipts bind adoption and cleanup to the original request. A reserved attempt
   can be cancelled atomically without VM work. An adopting/adopted attempt keeps
   capacity until physical cleanup is acknowledged.
3. Production vmmd requires the admission ID and verifies the complete deterministic
   request digest against its own registered node. It uses a private, fsynced local
   incoming journal and a stable per-instance file lock spanning adoption/cleanup.
   The journal binds each native physical generation before preparation effects.
   Cleanup first revokes delayed creation, then verifies the original native exit
   and resource-removal evidence. A missing/corrupt bound journal or different physical
   generation is not cleanup proof. Database acknowledgement may be retried without
   another VM operation. Ordinary boot cannot borrow an unretired migration
   identity. Once the original physical generation is fully retired, its cleanup
   proof is fsynced before ordinary preparation may reuse that instance ID.
4. Ownership commit validates the adopted receipt and admission generation, moves
   the instance/runtime evidence, preserves its admitted resource floors in resident
   accounting and consumes the reservation in one transaction.
   Accounting excludes the reservation only when the matching destination wake is
   published, avoiding a double charge within that transaction. Late commits after
   cleanup starts are fenced. Existing durable ownership resolution remains the
   authority after an uncertain commit.
5. Destination vmmd reconciles expired, closing-node and cleaning admissions after
   restart and on its bounded lease sweep. Source cancel/expiry and source-lease
   replacement refuse to resume or erase evidence while an inbound attempt is
   unresolved. The migration watchdog and direct source-row mutations also retain
   unresolved ownership. Cleanup failure retains the reservation; successful cleanup lets
   the existing source lease owner finish rollback. The scheduler never uses an
   unqualified Destroy(instanceID) to retire an admission-backed destination.
6. New protocol fields and a retirement RPC are internal. Production callers
   prepare destination wake lineage and cannot fall back to unfenced adoption.
   Existing tracker-only RPC fixtures and harnesses without prepared runtime
   lineage remain explicit legacy test seams; a registered production receiver
   refuses missing admission authority.

## Rollout and recovery

Stop old schedd migration/recovery dispatchers and vmmd migration receivers before
applying the migration. Settle or reconcile historical handoffs; their missing
destination records are not invented evidence. Upgrade receivers and dispatchers
together, preserve incoming/native journals, then resume. Pending admissions and
receipts block downgrade. Never clear a reservation because its deadline elapsed
or delete a local journal to force maintenance. Native migration, process-death
and leak qualification on dedicated x86_64 KVM hosts remains a deployment gate.

## Validation

Verify shared MemStore/PostgreSQL phase/replay semantics, immutable identities,
resource contention, both drain/reservation orderings, atomic ownership conversion,
fault rollback and source-expiry fencing. Exercise destination dispatch/cleanup
races, lost replies and daemon replacement with retained native journal evidence;
prove delayed adoption cannot follow a cleanup acknowledgement or destroy a newer
generation. Run scheduler/RPC/manager regressions, targeted races, SQLC/protobuf
generation checks, restored-schema parity, lint and daemon link checks. Record
portable evidence separately from native acceptance.

Portable validation passes: shared MemStore/PostgreSQL contracts, ordinary
admission contention, source/drain and commit/cleanup races, raw-writer guards,
suppressed/deferred transaction failures, full scheduler and RPC regressions,
targeted state/scheduler/manager/RPC races, SQLC/protobuf regeneration,
restored-schema parity, scoped lint and both daemon link checks. Admitted resource
bounds remain charged after ownership conversion; original retirement permits a
later ordinary wake without an old proof destroying its replacement.

The broader VM suite's 21 filesystem/source-mode/readiness failure families
reproduce with the incoming journal hooks removed. Its portable suite excluding
those failures passes. No `/dev/kvm` is available here; physical migration,
daemon/process-death and leak acceptance remains pending. Nothing deployed.
