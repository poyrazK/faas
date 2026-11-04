# ADR-20261103200001: GitOps service policy and scheduled Job handoff

- **Status:** accepted
- **Date:** 2026-11-03
- **Related:** ADR-570, ADR-20261103180001, ADR-20261103190000

## Context

The gateway resolved exact GitOps bindings but then pinned ordinary app policy.
An HTTPS alias with only a scoped declaration received an incomplete policy view
and failed authorization. Separately, retained workload-intent Job links kept
scheduled Jobs locked after source detach, preventing edits and quota cleanup.

## Decision

1. Treat a verified GitOps binding as frozen authorization. Pin its target,
   release, deployment, transport, call scope and reliability policy together
   with the live tenant-boundary check before wake or dispatch. Include the
   exact binding in the policy fingerprint. Ordinary app discovery and weighted
   routing cannot replace that graph's declaration or deployment selection.
   Keep protocol selection from the existing route contract: checked project
   graphs capture it; GitOps uses the app's protocol settings.
2. Store the exact owning environment source on each reconciled Job. A bound
   source protects settings in report mode and while suspended. Explicit
   detach releases customer edits and deletion while preserving the Job,
   execution history, frozen deployments and scoped intent. A replacement
   source does not inherit the old binding's ownership merely by being bound.
3. Keep unmarked N-1 Job links protected. New detach transactions attribute
   their legacy links to the retiring source. For already-detached N-1 rows,
   permit cleanup only when the Job predates a proven retired resource scope
   and no active replacement makes the old ownership ambiguous.
4. Reconcile released Jobs under a fresh controller lease during candidate
   preparation, including when adoption leaves an unchanged intent plan.
   Preserve an edited Job's identity; replace a deleted Job with a fresh one.
   Freeze the resulting Job identity in the new graph and acquire ownership
   atomically with preparation. Activation also stamps its source identity.
5. Retained released Job links carry no GitOps runtime authority. Source-backed
   artifacts remain protected; ordinary image selection may clear the former
   materialization, but cannot manufacture a new qualified source artifact.
6. Keep production enforcement and native qualification acceptance gates.

## Validation

Exercise HTTP/HTTPS scoped calls with account and declared app policies through
the production gateway wiring. Verify stable fingerprints, exact deployments,
release changes, tenant checks, HTTPS, method/path restrictions and conflicting
selectors. Cover detach/rebind, Job edits/deletion, fresh reconciliation and
deleted-Job replacement in both stores. Replay the Job ownership migration over
populated data, covering rows detached before and after upgrade, and reject SQL
ownership forgery or unverified source artifact publication.
