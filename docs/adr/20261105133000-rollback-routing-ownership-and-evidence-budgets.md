# ADR-20261105133000 · Fence app rollback routing and preserve migration evidence

- **Status:** accepted
- **Date:** 2026-11-05
- **Amends:** [ADR-601](601-binding-checked-historical-rollback.md),
  [ADR-259](259-revision-pins-and-project-release-sets.md),
  [ADR-20261104200000](20261104200000-rollback-serving-windows-and-retained-readiness.md).

## Routing ownership

An app rollback changes weighted traffic. An active project environment graph
can continue routing to a different deployment regardless of those weights.
Plain preparation and checked admission therefore refuse such a rollback with
`project_release_rollback_required`. Both publication paths check the current
graph again under the app lock. A graph published while readiness was pending
cannot turn a successful receipt into a false recovery claim. The legacy default
lane also checks the project's production graph. Project release recovery keeps
its existing atomic graph workflow and retention guarantees.
Preview apps retain their independent weighted routing.
Graph publication advances the existing code-admission epoch, so a rollback
transaction retries a repeatable-read snapshot taken before that publication.
The fallback boundary is registered in the clone schema catalog; target graphs
establish their own serving history when published.

A checked operation's service flag must still match the app's execution mode at
cutover. A changed mode invalidates the intent instead of choosing the previously
captured function path and bypassing service readiness. The worker records a
terminal mode-change failure, allowing the operator to submit a new operation.
Existing ready-service capacity and gateway acknowledgement barriers remain in
force.

## Revision retention

Every new serving period receives a fresh retention deadline when it is replaced.
Checked cutover, ordinary stable publication, and service handoff extend pins for
traffic-bearing predecessors with the later of the new deadline and any longer
existing promise. Already-retained zero-traffic siblings do not renew their
deadlines on another rollout or handoff retry. Cleanup timing cannot determine
whether a newly retired revision URL remains available.

## Receipt budget

The 16 KB checked receipt limit applies to its public projection. Exact internal
`_release_review` acknowledgements are excluded from that budget and must remain
an array or legacy JSON null. They retain the existing private representation,
which older control planes already read and preserve. No migration history is
truncated or implicitly acknowledged. Public status decoding continues to omit
this internal evidence. Other public fields remain size-limited.

Three migrations add the new receipt check as NOT VALID, validate it, and then
drop the old constraint, each in its own transaction. Apply data before
control-plane changes. Rolling the data definition back reinstates the old check
as NOT VALID, preserving already-written historical evidence; control-plane N-1
can operate against the expanded definition without a data downgrade.

## Validation

MemStore and PostgreSQL regressions cover graph checks at admission and cutover,
legacy production scopes, mode changes in both directions, repeated plain and
checked rollback pin windows with delayed cleanup, and 160 acknowledged release
commands across receipt updates. PostgreSQL tests retain the public size and
private-type checks. API tests verify the project-recovery conflict and terminal
mode-change status. The previous serving-history regression file has a neutral
filename so its ten tests are discovered on Linux.
