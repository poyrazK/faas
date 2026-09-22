# ADR-193 · Transactional per-node RAM reservation

- **Status:** accepted
- **Date:** 2026-09-21

## Context

Invariant §6.2-2 requires that Σ(`ram_mb` + `PerVMOverheadMB`) over the live
instances on a compute node stays at or below that node's
`admission_ceiling_mb`. Until now the only thing enforcing it was
`sched.NodeLedger`, an in-memory mutex whose package doc is explicit about
its premise:

> schedd is the single writer to the instances table and a single process, so
> this in-memory accounting needs no distributed locking — just a short-held
> mutex.

ADR-062 retired that premise. Every schedd owns a shard of apps
(`apps.node_id`), `SeedLedger` rebuilds reservations only for the apps the
local schedd owns, and `ChoosePlacement` may put an instance on **any** active
node. So schedd A's instances on node C are structurally invisible to schedd
B's ledger, and the per-node ceiling check in `NodeLedger.Admit` compares a
request against a partial view.

The remaining cross-schedd signals are both advisory:

- vmmd's `CapacityReport` stream is authoritative about physical usage, but
  each vmmd dials a single `scheddTarget` — its node-local schedd. A given
  schedd therefore has a fresh 1 Hz sample for its own node and nothing for
  its peers.
- `ComputeNodeUsedMBByNode` is the fleet-wide fallback, read through
  `NodeUsageCache` with `NodeUsageFreshness = 1s`.

That leaves a read-then-insert race with no backstop, and nothing at the
schema layer: `compute_nodes.admission_ceiling_mb` carries only
`check (admission_ceiling_mb > 0)`. Two schedds reading the same cached
headroom inside one second each admit against the same free MB. A reproduction
is committed as `TestPgStoreNodeReservationSerializesConcurrentAdmits`: with
the lock removed, 16 concurrent admissions against a 1024 MB node admit 14 and
leave the node at 3584 MB — 3.5× its ceiling. This is the shape behind the
2026-09-03 vmmd OOM and the cold-burst 504s.

## Decision

Enforce the per-node RAM ceiling in the same transaction that publishes the
reservation.

The INSERT into `instances` is already the point at which a reservation
becomes visible fleet-wide — `ComputeNodeUsedMB` sums exactly those rows — so
`PgStore.insertInstanceWithNodeReservation` wraps the headroom check and the
insert in one transaction holding `pg_advisory_xact_lock(class, hashtext(node_id))`.
Admissions to the same node serialize; admissions to different nodes do not
contend. The lock uses PostgreSQL's `(int4, int4)` advisory space, which is
disjoint from the single-bigint space every other advisory lock in `pkg/state`
uses, so a node-id hash can never collide with an app-id hash.

All three instance insert shapes take the guard: `CreateInstance`,
`CreateInstanceWithMode`, and `CreateJobInstance` (a job task is resident RAM
on the node like any other instance — `ComputeNodeUsedMB` does not filter by
kind). `MemStore` mirrors it under `m.mu`.

Three deliberate boundaries:

- **The ledger is not replaced.** It stays the fast local path and remains the
  only enforcement for per-app concurrency (§6.2-1), vCPU budget, and CPU
  millicores. This ADR adds a durable floor under the RAM ceiling alone.
- **The counted state set matches the chooser, not `State.CountsForRAM()`.**
  `CountsForRAM` also returns true for `snapshotting` and `migrating`, while
  `ComputeNodeUsedMB` sums only `('waking','cold_booting','running','warm')`.
  The guard must agree with the chooser: counting a state the chooser ignores
  would make a node the chooser believes has headroom refuse every wake routed
  to it. Reconciling the two predicates changes placement behaviour and needs
  its own ADR.
- **A row that holds no resident RAM is not guarded.** `parked`, `pending`,
  and terminal rows run as a bare pool insert exactly as before — no
  transaction, no lock, no behaviour change.

`state.ErrNodeCapacity` is translated by `Engine.nodeCapacityProblem` into the
same `*api.Problem` (`CodeCapacity`, HTTP 503) the ledger already returns for a
RAM refusal, so the gateway, the CLI exit code, and the customer-visible error
are unchanged. Node id and MB figures stay in the operator log rather than the
customer-facing detail.

## Consequences

Invariant §6.2-2 becomes an enforced property of the database rather than an
assumption about process topology. A schedd restart, a second schedd, a
warm-pool fill, a snapshot prime, and a job dispatch are all bounded by the
same ceiling, and no future caller can reintroduce the race by adding a
placement path — the guard sits under the insert, not beside it.

The cost is one short transaction per RAM-consuming admission: an advisory
lock, one aggregate over `instances_live_node_id_idx` (whose partial predicate
is exactly the four counted states), and the insert. Admissions to one node
serialize, which is the intended semantics; `TestPgStoreNodeReservationIsPerNode`
pins that a full node refuses only its own admissions.

Capacity refusals move slightly earlier in the wake path — before
`acquireHostPortLeases` and `ledger.Admit` rather than at the ledger — so a
refused wake now unwinds with no row written and no host-port lease taken.
That is strictly less cleanup than the previous path performed.

## Follow-up work

- **Ownership transfer is not covered.** Live migration moves an instance by
  UPDATEing `node_id` (`MigrateInstanceOwner`), not by inserting a row, so it
  can still push a destination node past its ceiling. The same guard belongs on
  that UPDATE.
- **A covering index** — `(node_id) INCLUDE (ram_mb)` on the live-state partial
  predicate — would make the in-lock aggregate index-only and shorten the
  critical section. Deferred: it needs a migration slot and the current index
  already narrows to the live rows on one node.
- **`CountsForRAM` vs the node-usage SQL** disagree about `snapshotting` and
  `migrating`. Reconciling them is a placement-behaviour change and needs its
  own ADR.

## Rejected alternatives

- **`SERIALIZABLE` isolation on the admit transaction.** Correct, but it turns
  a bounded per-node wait into fleet-wide serialization failures that every
  caller must retry, on the latency-critical wake path.
- **A `node_reservations` table with a `CHECK`.** A constraint cannot express
  a sum over sibling rows, so this becomes a trigger — enforcement hidden from
  anyone reading the Go, and a per-row trigger cost on the hottest table.
- **Making the ledger distributed.** Replaces one in-memory view with a
  consensus problem ADR-025 explicitly declined; the durable row set is already
  the shared truth.
- **Widening vmmd's capacity stream so every vmmd pushes to every schedd.**
  O(nodes²) connections, and still advisory — it shrinks the race window
  without closing it.
