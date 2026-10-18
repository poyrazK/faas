# ADR-1528: Node admission generation fencing

Date: 2026-10-17
Status: Accepted; implemented and verified by portable checks

## Context

Placement filters active/recovering nodes from a notification-backed snapshot.
Instance insertion later reserves RAM without rechecking the node lifecycle or
the lifecycle generation selected by the chooser. A drain can complete between
these operations and a delayed placement can then insert new resident work onto
the maintenance node. Migration destination reservations have the same gap.
ADR-1527 makes completion atomic but intentionally leaves admission policy with
its existing owner.

## Decision

1. Placement carries the selected node's lifecycle generation. A typed context
   binding carries that decision through existing store entry points without
   expanding the broad Store interface. It binds both node ID and generation;
   malformed/mismatched bindings fail closed. The scheduler never substitutes a
   newly read generation for an earlier selection.
2. Every resident instance insert and migration destination admission rechecks
   current lifecycle under the same transaction as the insert/ownership change.
   With a placement binding it also requires the captured generation. The
   capacity-policy lock comes first, followed by the node and existing reservation
   locks. Disabled capacity protection retains shared policy locks; enabled
   protection retains exclusive locks. A node share lock holds the decision stable
   until commit. MemStore mirrors this under its mutex.
3. A database trigger independently rejects new residency on a non-admitting node
   for raw SQL, specialized writers and legacy entry points. It covers resident
   inserts, destination changes and nonresident-to-resident transitions. Existing
   resident transitions on the same node remain legal so recovery and cleanup can
   finish. Direct entry points without a prior placement binding perform a fresh
   lifecycle admission, rather than claiming generation-fenced cached placement.
   MemStore's existing unregistered-node fixtures remain supported only without a
   binding; PostgreSQL still requires a real node through its foreign key.
4. Wake, prime and warm-pool fills retry only a definite stale-placement refusal,
   at most three times. Each refusal releases its restore-pressure reservation;
   retries bypass the node cache and select from current database rows. No VM call
   or ledger admission occurs before a successful instance insert. Job dispatch
   releases its preclaimed lease and ledger slot before the equivalent bounded
   retry; its queued task and instance insert remain atomic. Specialized reviewed
   qualification admissions retain private-node restrictions and validate the
   binding in their own transaction; their owner handles retry.
5. Migration captures its destination lifecycle/generation before pausing the
   source and validates it again in the ownership transaction. A later lifecycle
   change uses the existing durable commit resolver and adopted-VM cleanup path;
   it never retries a VM side effect merely to repair stale placement. The
   resolver takes capacity before its instance lock to preserve lock order.
6. Nonresident records and already-resident runtime publication keep their
   existing semantics. This adds no VM RPC, migration protocol or placement
   preference, and does not change resource quotas.

## Rollout and recovery

Quiesce old schedulers, including job and migration dispatchers, before migration
and resume upgraded writers together. The raw lifecycle guard prevents future
closed-node admissions but does not rewrite old inventory or prove historical
evacuation. Inspect resident rows on non-admitting nodes before taking hosts out
of service. A downgrade requires the same coordinated stop; ADR-1527's existing
generation/receipt preservation rules remain in force. See the
[admission rollout guide](../ops/node-admission-fencing.md).

## Validation

Memory/PostgreSQL tests pass for lifecycle and generation refusals, same-state
cycles, all resident states, legacy/raw SQL, jobs and migration destinations.
Both admission/drain orderings, capacity policy lock modes, rollback and
statement/commit failures pass. The full scheduler suite passes, including fresh
reselection, bounded retry, restore-pressure and job lease/ledger cleanup, and
migration rollback after a destination cycle changes.

Targeted memory-store, admission/completion/recovery scheduler, event and
statetest race checks pass, as do scoped production/test lint, SQLC diff and
restored-schema parity. Schedd links successfully. Nothing deployed. This
environment has no KVM device; native migration and evacuation acceptance remains
a separate fleet gate.
