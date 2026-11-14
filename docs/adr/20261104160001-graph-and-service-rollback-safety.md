# ADR-20261104160001 · Guard graph and service rollback publication

- **Status:** accepted
- **Date:** 2026-11-04
- **Amends:** [ADR-1448](1448-releases-that-change-the-database.md),
  [ADR-601](601-binding-checked-historical-rollback.md), and
  [ADR-20261104140000](20261104140000-rollback-execution-fences-and-outcomes.md).
- **Decision:** Enforce rollback contract and migration guards for graph
  restoration, validate service abort migration history at traffic publication,
  and reconcile checked service receipts from authenticated completed handoffs.

## Graph restoration

Environment-promotion rollback compares the captured contracts of the actual
active and restored graph members, including fallback deactivation. A weighted
fallback deployment may differ from the deployment selected by the active graph.
Production contract failures retain the existing 422 response and stable code.
The graph's conditional publication still rejects a changed active graph.

Native graph publication checks release history for changed members previously
served by a graph. Its cutoff is the most recent graph serving end, derived from
that graph's expiry minus its TTL. Unpromoted canary retention deadlines are
excluded because they do not prove a completed default-serving interval.
Weighted fallback traffic can remain at 100%
through a graph switch, so it is not evidence that the old member served a later
migration. Deactivation to a weighted fallback uses the active graph's creation
as its cutoff. Unchanged members and new artifacts do not acquire rollback
acknowledgements. Managed GitOps keeps its separate qualification workflow.

These checks run under the existing app locks, which also serialize release-task
starts. A release-task start additionally advances the existing per-app admission
guard generation. Repeatable-read config transactions lock that guard and retry
if their history snapshot predates the migration. This adds derived admission
state, without changing app intent or introducing a schema migration.

## Service abort and completion

Both initial abort traffic publication and legacy abort finalization check fresh
predecessor release history before restoring traffic. An abort accepted before a
later migration does not authorize that migration crossing. The current release
keeps serving when the check fails; binding qualification exposes the existing
rollback migration error codes for review.
Once the predecessor already serves 100%, finishing the abort does not restore
code and may complete even if a migration starts during draining.

A checked service rollback completes from its matching durable handoff: promote
completed, the predecessor matches, and the passed binding evidence has this
operation's request, recipient and routing audit identities. Later deployments
may supersede or retain that revision at zero traffic. They do not reverse the
historical success. Both stores recheck the proof under their mutation locks,
prefer completion over a stale failure verdict, and preserve its actual handoff
completion timestamp. Completing the receipt releases the active-scope reservation
so a subsequent incident can request another checked rollback.

## Validation and rollout

Regression tests cover MemStore and PostgreSQL, graph and fallback serving
windows, migrations accepted before and after switches, service abort races,
retained and superseded completion, stale verdicts and handoff identity mismatches.
HTTP tests verify refusal codes and continued recovery after receipt reconciliation.
No new API fields, quotas or migrations are required. Deploy the updated control
plane and scheduler using the normal release order.
