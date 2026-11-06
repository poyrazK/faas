# ADR-20261104110000: Project release clearing and environment deletion

- **Status:** accepted
- **Date:** 2026-11-04
- **Owner:** apid and state maintainers
- **Amends:** ADR-20261103194624, ADR-20261103222000

## Problem

Rolling back a promotion without a predecessor used ordinary deployment failure
recovery. When its serving candidate shared the scope with a dark revision, that
recovery activated the dark revision. The handler's read could also become stale
before the failure mutation.

Environment deletion refused live deployments but allowed pending builds. Its
cascade erased their pinned workload settings, causing runtime resolution to use
the shared production app configuration. A deployment could become live after
its stage was removed. Checked release preparation and binding verification also
used project-wide membership even though graph publication is scoped.

## Decision

Clearing a promotion without a predecessor uses a dedicated checked store
operation. Candidate ownership, live status, unsplit traffic and the absence of
every other live revision in its scope are verified inside the mutation boundary.
Dark siblings produce a conflict. A successful clear does not rebalance traffic
onto a different revision. Other scopes retain their existing serving state.

Environment deletion requires all deployments in the scope to be terminal:
failed, cancelled or superseded. Pending, building, imaging, snapshotting and live
deployments block deletion without changing their settings or scoped values.
Project deployment creation and activation also check and lock the registered
stage, so stale request preflights cannot admit releases into a deleted stage.
Standalone app scopes and legacy production/default behavior are preserved.

Deleting a stage also soft-deletes its terminal deployments in the same
transaction, preserving their status and administrative audit history. These
releases cannot be selected or prepared for rollback, activated, or resolved
through the legacy app-settings fallback after their settings pins are removed.
Recreating the slug gives it a new environment identity and empty membership;
it cannot inherit retired deployment history. Releases in other scopes remain
available. No deployment artifacts or provider data are deleted by retirement.
Late terminal failure callbacks may finish historical records, but failure
recovery never rebalances traffic for a retired deployment or a missing row.

A pending managed-resource cleanup job reserves its deleted environment's slug
against both direct creation and cloning until cleanup completes. Expired worker
leases and retry delays do not release this reservation. PostgreSQL checks it
under the existing project lock; MemStore checks it under its mutex. The API
returns `environment_cleanup_pending` so callers can retry after cleanup finishes.

Configuration clones, source-value capture, binding preparation, workload settings,
secret references and scoped policies use the source environment's membership.
Production excludes stage-only identities; a named stage copies only its admitted
workloads. The captured membership and values still fence changes between clone
preparation and commit, including newly admitted source services.
For a legacy production identity without a desired head, a successful
configuration clone records the source's unchanged current settings atomically
with the target. Its first cloned stage head cannot reclassify the source as
stage-only. Existing source heads and runtime deployment pins are preserved.

Checked preparation, activation, rollback, deactivation, verification review,
verification runs, diagnostics and release staging share the existing environment
membership rule. A service confined to another stage is irrelevant; a service
newly admitted to the target still invalidates an incomplete manifest. Store
verification snapshots apply the same membership under their mutation locks.
A function-only migration applies that membership to PostgreSQL checked graph
authorization and deferred deactivation checks. It retains the existing binding
evidence, readiness, ownership, routing and expiry guards. Apply the migration
before updating the control plane; no guest protocol or table change is required.

## Validation and recovery

MemStore and PostgreSQL regressions cover a serving or dark promotion candidate
with an existing or concurrently staged sibling, repeated successful clearing,
scope isolation, each deployment phase at deletion, stale deployment creation
and activation, and scoped preparation and recovery. HTTP tests exercise checked
activation and publication with another stage's service and deletion after a
pending deployment is cancelled. Verification tests cover scoped review, admission
and completion. Checked store tests commit activation, rollback and deactivation
with an unrelated stage member, and PostgreSQL tests exercise migration down/up
and repeated up. Existing deployments and managed resources retain their identities.
