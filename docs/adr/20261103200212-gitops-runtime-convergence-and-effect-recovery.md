# ADR-20261103200212: GitOps runtime convergence and effect recovery

- **Status:** accepted
- **Date:** 2026-11-03
- **Related:** ADR-568, ADR-1155, ADR-1331

## Context

Owned source and runtime fields remained unconditionally unqualified after a
graph completed qualification, activation and serving. The controller could
never finish that graph as converged. Separately, a newer approved definition
could omit a workload whose committed runtime refresh was unfinished. Recovery
looked only at desired workloads, so it could no longer observe or complete the
original effect, blocking every subsequent reconciliation and source detach.

## Decision

1. Separate physical fleet freshness from whole-graph completion. Ownership
   requires evidence for the current approved revision, generation, intent
   version, plan and exact active release. Clear its workload blocker only when
   that graph has preparation, qualification, activation and serving proof with
   no remaining blockers. Missing, stale or partial evidence stays unready.
2. Check both conditions again when finishing a converged run. Current serving
   evidence cannot excuse stale residents, starting instances, stale snapshots
   or pending effects. Advance the applied revision only after those checks.
   Include source/runtime, schedule, service and queue ownership in PostgreSQL's
   physical runtime-target view, matching the in-memory store. An additive view
   migration preserves its existing freshness predicates and result columns.
3. Recover a committed runtime effect against its original app and environment
   under the current source lease, independently of later desired membership.
   Preserve its freshness boundary, snapshot invalidation, durable refresh
   request and completion rules. Omission does not cancel work or acknowledge a
   stale resident. Report mode may observe completion but never request refresh.
4. Keep production enforcement and native qualification gates unchanged. These
   changes use existing state and receipts, with no protocol or stored-row change.

## Validation

Exercise both stores through prepared, qualified, activated, partly acknowledged
and fully serving stages. Verify convergence advances the applied revision only
after all members and gateways have serving proof, and changed intent invalidates
that proof. Approve removal while an earlier refresh is pending, covering cold
and resident fleets, report mode, superseded leases, original-scope drain and
subsequent source detach. A fully serving graph with a starting or unacknowledged
resident must still fail convergence, including a workload without any managed
variables. Keep existing runtime freshness and recovery tests.
