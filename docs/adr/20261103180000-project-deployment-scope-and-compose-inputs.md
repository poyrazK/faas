# ADR-20261103180000: Project environment deploys and complete build inputs

- **Status:** accepted
- **Date:** 2026-11-03
- **Owner:** apid and repository scan maintainers
- **Amends:** ADR-20261027120000, ADR-20261102192943, ADR-590

## Problem

An ordinary project apply used production reconciliation for every environment.
A staging source that omitted a service could delete its production app. A
stage build also advanced the app-wide accepted source digest, causing the
subsequent production apply of that source to enqueue nothing. Separately,
workload-only hashing missed repository build inputs, folded migration steps
lost their environment defaults, and mixed Compose environment spellings lost
inherited variables during override merging.

Atomic production reconciliation also left existing saved workload heads
stale, so deployments pinned old runtime settings and source checkpointing
could revert resource changes. Production omission could delete a service
created only for staging. Folded image migrations also discarded their
explicit entrypoint and secret-file mounts.

## Decision

Production applies retain ownership of existing app identity, production
configuration, removal and the production source checkpoint. Named stages
reserve new app identities under the existing atomic quota transaction, then
write selected services' source-owned settings into the existing immutable
environment workload revisions. Deployment creation pins those settings.
Existing production apps and omitted services are preserved. A shared service
rename requires a production plan. Stage applies build every selected service,
including unchanged source, without advancing the production checkpoint.
Production routes and triggers are not reconciled by a stage apply; the plan
reports their preservation. Protected-stage plan approval remains required.

Production reconciliation advances an existing production workload head with
the source-owned fields in the same transaction as its app row. Environment-owned
work policies and queue bindings are retained. MemStore prepares revisions
without publishing them until all transaction validation passes; PostgreSQL
publishes them inside the reconcile transaction. Existing deployments continue
to pin their original revisions.

A service with a named-stage workload head and no production source checkpoint,
workload head or deployment is stage-only. Production plans and reconciliation
preserve those identities and continue counting them toward quota. The store
rechecks this under the app lock to reject a stale destructive plan. Explicit
production selection and deployment admits the shared identity to production;
subsequent production omission retains the existing shared-app removal policy.
Stage-only apps can be explicitly deleted when their capacity is no longer needed.

Source hashing covers the actual staged build context. Repository-context
workloads include root lockfiles and shared source. Compose Dockerfile contexts
retain their narrower boundary. Tree digests are cached within one scan so a
repository shared by many services is read once. This changes the digest
format and causes one refresh deploy for previously accepted sources.

Folded migration steps supply environment defaults to their owning service
before import and managed-database consumer resolution. Explicit owner entries
win. Values stay outside scanner results and plan responses, and existing
secret and opt-in import restrictions still apply. A missing source-specific
release command cannot erase a folded Compose release command.

An explicit prebuilt migration entrypoint is composed with its command before
folding, using the same argv and shell normalization as built services. Migration
secret-file mounts are merged into the owner's mount collection. Identical
sources and effective permission modes deduplicate; conflicting mounts and a
combined collection exceeding the existing limit fail the plan. These files
are also available to the serving owner because a release command shares that
owner's runtime manifest; separate task-only mounts are outside this change.

Compose environment map and list spellings are normalized and merged by key
for overrides, includes and extends. A bare list key keeps its host-value
semantics; an explicit override wins over the inherited entry.

Free continues to support one-service projects under its existing one-app
quota. Multi-service projects require Hobby or higher and available app
capacity. The capability description and customer guide state this; there is
no financial-model or quota change.

## Validation and recovery

HTTP regressions cover stage omissions, stage-first deployment of a new image,
scoped runtime settings and stage service additions. Scanner regressions cover
shared build inputs, context boundaries, migration imports and all environment
map/list merge combinations. Existing production apply and source retry tests
remain applicable. No schema migration or VM lifecycle change is required.

MemStore and PostgreSQL contracts additionally cover production settings
advancement, historical deployment pins, rollback on validation failure,
staging-only preservation and explicit production admission. API regressions
cover production deployment pins, omission previews and preserved app quota.

Rollback uses the prior apid and CLI release, but reintroduces the scope,
settings synchronization, migration folding and hashing defects. Suspend project
applies while running that control plane; existing deployments retain their
pinned workload revisions and artifacts.
