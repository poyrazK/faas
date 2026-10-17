# ADR-1548: Signed artifact promotion and rollback for scheduled Jobs

- **Status:** accepted (implementation preview; native acceptance pending)
- **Date:** 2026-10-17
- **Related:** ADR-1542, ADR-1544, ADR-1545, ADR-1546, ADR-1547

## Context

Services can reuse a signed source build during reviewed promotion and recovery,
while source-backed schedules still require a new build from the manifest commit.
This prevents one reviewed release workflow from covering services and Jobs.

## Decision

1. Allow source and Dockerfile scheduled workloads to select ADR-1544's producer
   ID and input hash. Keep the same account, project, mapped app, recipe and
   provider/repository trust scope. Cross-app transfer, sidecars, function
   schedules and Job queue bindings remain unsupported.
2. Give an artifact-backed Job an internal source reference containing its
   environment, app, producer ID and input hash. A commit-backed reference and
   an artifact-backed reference are disjoint. Artifact selection requires a null
   source revision; the target manifest commit and original build commit remain
   distinct. Go and SQL validate the same shape and frozen materialization receipt.
3. Preparation creates a held target candidate with the retained application
   layer and no archive request or new build. Normal approval pins bytes and
   provenance through the existing GC fence. Fresh target Job qualification is
   mandatory. Activation binds the paused Job to the target deployment and layer,
   advances the schedule revision and resets its scheduling cursor when needed.
4. Promotion and rollback change only compatible source contracts. Require a
   schedule on both origin and target and preserve the target cron/timezone,
   production argv, smoke command, variables, secret references, bindings and
   policies. The existing CLI/SDK preview/export and recovery catalog carry the
   same immutable artifact evidence and review hash; no new execution endpoint
   or approval authority is introduced.
5. Schedd authenticates the borrowed producer's current publisher signatures and
   base lineage before cold boot and again while the guest start gate is held.
   Retention does not grant trust. The active release, frozen target configuration
   and exact occurrence snapshots remain required. Revocation fails closed.
6. Historical Job serving evidence selects code for future executions. It never
   replays completed runs, transfers execution receipts or satisfies a new
   release's serving state. A matching successful post-activation occurrence of
   the current schedule revision is still required. Rollback restores no data.
7. Install the SQL adapter atomically with a schema-scoped completion guard so
   migration-ledger replay preserves subsequent function refinements and all
   existing Jobs, runs, pins and serving history.

## Validation boundary

Portable signed byte fixtures and memory/PostgreSQL journeys cover no-build
promotion, configuration isolation, fresh qualification/occurrence requirements,
reviewed rollback/catalog selection, identity mismatch, revocation and GC fences.
Scheduler guard regressions cover repeated current publisher validation and
mixed commit/artifact identities. Migration checks exercise fresh apply, exact
ledger replay and canonical reference refusal cases.

These are control-plane and fake execution receipts, not native acceptance.
Exclusive physical x86_64 Linux KVM VM/leak acceptance remains pending. Production
qualification dispatch and enforcement remain gated; no release or deploy occurs.
