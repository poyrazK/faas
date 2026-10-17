# ADR-20261017190805: Reviewed GitOps rollback proposals

- **Status:** accepted (implementation preview; dedicated native acceptance pending)
- **Date:** 2026-10-17
- **Related:** ADR-1331, ADR-20261017190803, ADR-20261017190804

## Context

Retained release sets, serving receipts and signed build layers exist, but
operators must reconstruct source references to propose a code rollback.
Historical qualification must never authorize a new activation, and restoring
an old manifest wholesale can accidentally revert secrets, routes or policy.

## Decision

Add a read-only rollback-plan endpoint and CLI for one explicitly selected
release UUID in the current bound environment. Read the immutable release,
retained graph/revision and completed serving receipt on the same snapshot as
current target intent. Verify exact receipt identity, recorded route/consumer
acknowledgements and current app mappings. Rebinding creates a new source;
releases belonging to another source, project, account or environment are rejected.

Only compatible workload source contracts change. Current variables, secret
references, runtime settings, routing, policy, queues, bindings, schedules and
additional workloads remain intact. Missing workloads or changed app identity
block export; rollback does not recover retired identities implicitly. Scheduled
Jobs and sidecars are excluded initially.

Reuse ADR-20261017190804 artifact references for source, Dockerfile and function code.
Allow distinct deployments in the same app/environment to borrow an original
producer. Artifact selection explicitly clears the derived `source_revision` to
null while retaining its ownership; it does not enable general pruning. The
observation also records null so repeated reconciliation converges without a
new build at the target manifest commit. Keep current signature, selected-root, repository, base-lineage and
retention fences; do not transfer trust or copy snapshots. A selected source-build
producer must predate the historical graph’s completed preparation; later
replacement producers block export rather than changing historical code. Immutable images
retain their reviewed digest. Historical artifacts may be unavailable; preview
never extends retention or resurrects collected data. Git approval pins source
build artifacts before candidate preparation through the existing GC fence.

Use a separate rollback hash namespace binding selected release, graph/revision,
serving completion, artifacts, current active release, target commit/digest,
source generation/intent and ownership plan. A changed observation rejects an
expected hash. Patch and manifest exports require a fresh matching review hash;
manifest export also checks the complete local base and proposed digests.
Responses and patches omit configuration and secret-reference values. A proposal
is not approval or execution authority. Normal Git review, target-specific fresh
preparation, qualification, activation and serving remain mandatory.

## Consequences and validation boundary

This is code rollback, not database/data restoration or automatic incident
remediation. Compatibility of old code with current dependencies still needs
review and fresh qualification. No live mutation or privileged native dispatch
is added. Portable tests and PostgreSQL control-plane receipts cannot establish
physical VM or leak acceptance. Production qualification/enforcement stays gated
until dedicated physical x86_64 Linux KVM acceptance passes.
