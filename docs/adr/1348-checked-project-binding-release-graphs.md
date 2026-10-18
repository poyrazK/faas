# ADR-1348: Checked project binding release graphs

- **Status:** Accepted
- **Date:** 2026-10-13
- **Context:** Enforced binding policies intentionally block generic release-set
  publication and deactivation. Per-deployment verification cannot qualify a
  proposed internal service edge: the original safe HEAD probe accepts any
  healthy replica of the target app.

## Decision

Add a metadata-only preparation endpoint and an explicit checked activation
endpoint. Preparation emits a JSON manifest containing every exact candidate,
the expected active release (or exact weighted fallback), policy revisions and
internal service targets. A manifest is selection intent, never an evidence
grant. No draft release row or serving route is created during preparation or
qualification. Candidates must already be materialized, live and eligible.

The CLI verifies all member plans before admitting tasks, then uses the unified
policy verifier for each member. Every member scope must have an enforced
policy. Existing storage write consent, queue readiness limits, application
acknowledgements, freshness, snapshot confirmation and private outbound identity
checks remain in effect. The prepared file fixes policy revisions for the run.

A V2 reserved service probe accepts an exact target deployment UUID. APID checks
the declared service, tenant, live artifacts and caller scope before pinning the
probe. The guest sends a TLS-verified HEAD to the platform probe route and
requires an exact target echo. The gateway validates target ownership/live state
and filters healthy endpoints by deployment. It neither wakes nor invokes the
service handler. Durable evidence is partitioned by target: an old recipient or
ordinary any-replica probe cannot qualify a candidate graph, and candidate proof
cannot replace ordinary inventory evidence. Service edges outside the proposed
project retain their ordinary verification requirements.

Activation independently derives the internal edges and evaluates every member
against current stored policy. It holds all managed PostgreSQL domain fences
and all root catalog fences through one graph transaction. Complete membership,
active release/fallback CAS, artifacts, retention eligibility, policy revisions,
evidence deadlines and desired service replica counts are rechecked under
locks. PostgreSQL authorizes only the exact retiring and replacement release
IDs and validates the complete member map with a deferred constraint trigger.
Expiry is checked again at graph writes and commit. MemStore checks the same
conditions before any mutation. A deadlock or serialization conflict aborts the
transaction; callers must requalify before retrying.

## Consequences

Forward activation under enforcement becomes available without temporarily
turning policies off. Existing generic publication/deactivation stays closed.
Prepared files remain useful for review but may become stale; activation always
rechecks them. No VM ownership or lifecycle changes are introduced by this ADR.
ADR-1349 adds checked rollback to retained history; ADR-1350 adds checked graph
deactivation; ADR-1351 adds temporary non-GitOps service readiness.
GitOps-managed candidate staging requires a separate checked workflow; this API does not lift candidate holds or replace reviewed
GitOps membership. Single-deployment checked rollback remains available.
