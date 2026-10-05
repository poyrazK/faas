# ADR-597: Immutable targets for runtime upgrade builds

Status: accepted · 2026-10-05

## Context

ADR-596 records the base that a build actually used and previews published
targets. A controlled updater must also select the exact base before building.
Using the current daemon default can change the selected OCI source. Selecting
only an OCI digest is insufficient: identical OCI input with a different
guest-init or layout produces a different runtime release.

## Decision

Add an internal, apid-owned preparation seam that pins one published runtime
release to a fresh managed-function source deployment before a build is queued.
The caller supplies the expected source archive checksum. Pinning checks that
checksum, the runtime family, the active app and source deployment kind, and
refuses customer Dockerfiles, containers, unknown source evidence and already
materialized or queued builds. Pinning and queue admission serialize on the
deployment row. Repeating the same pin before queueing is harmless; a different
target cannot replace it.

Persist the source checksum, build root, byte count, deployment kind and handler
alongside the target. Database triggers prohibit changing these source inputs
or deleting/replacing a target while its deployment exists. Both stores check
the source inputs on reads. A failed deployment's ordinary retry copies the
same target in the retry transaction; resetting execution state cannot select
a new daemon default. Deleting the parent deployment can clean up its pin. The
migration is forward-only so retained attempts keep their chosen identity.

Builderd reads the pin before choosing its Railpack runtime and cache recipe.
Use the selected release's OCI source, validate the family and builder host
architecture, and record that source in fenced build completion. A lookup
failure cannot fall back to daemon defaults. Ordinary unpinned builds retain
their current selection behavior.

Imaged requires successful-build evidence for both the selected OCI source and
the captured source archive checksum. Reuse the exact catalogue ID and verify
its published base bytes and required base paths. Require a bounded, readable
scan sidecar for the selected OCI source, with a timestamp and both findings
projections; scanner-failure placeholders are refused. This does not replace
vmmd's vulnerability admission policy or establish upgrade compatibility.
Missing or corrupt selected bytes cannot be regenerated as another identity.
Installed guest-init, layout and default-reference changes must not select a
different target. Retried imaging must agree with the existing artifact
binding, and the state layer refuses a binding to any other target.

After function assembly, confirm the physical artifact's binding rather than
staging the mutable daemon default. Explicit updates require immutable base
staging; the legacy test/materialization path cannot silently bypass it.
Runtime release replication remains the ADR-596 handoff before artifact
binding. Source-root and handler capture are each bounded to 4,096 bytes by
`api.RuntimeUpgradeSourceFieldMaxBytes` and database checks.

This is build preparation infrastructure, not the update executor's authority
to deploy. No customer mutation route or apply control is added, and upgrade
previews continue to report execution unavailable. The subsequent apid executor
must capture the serving predecessor, configuration and secret versions,
require operator qualification of the selected target, rebuild the retained
source, obtain fresh candidate cold-boot/readiness evidence, and use the
existing guarded rollout and rollback. This seam alone cannot claim that those
gates have passed. Publication and scan evidence remain distinct from native
runtime qualification.

## Consequences

The build and imaging boundary now accepts an exact reviewed runtime without
changing the serving release or modifying a logical base. Same-source targets
with different guest-init or layout can reuse source-compatible build exports
while producing a new physical application artifact bound to the chosen base.
Cache recipes still include the exact OCI source; injected base components are
selected during imaging and cannot be inferred from a cache hit.

App configuration and secret capture, target qualification records, customer
apply controls and scheduled maintenance remain subsequent executor work.
Do not expose the internal pin operation directly as an apply endpoint.

## Validation

Memory and PostgreSQL tests exercise immutable selection, source mismatch,
queue races, unsupported builds, exact-target artifact binding, retry retention
and direct SQL integrity fences. Builder tests run the real orchestrator with a
fake VM and verify the chosen source and completion provenance. Image tests
change guest-init and daemon defaults, then verify retention of the selected
ID; missing build/ref/source/scan evidence and corrupt bytes fail closed.

Changes to runtime materialization still require `test-metal` and `leakcheck`
on a dedicated native x86_64 KVM acceptance host before deployment. Local tests
on macOS do not satisfy those gates. The previously configured acceptance
project is suspended; no native qualification is claimed by this change.
