# ADR-145: Fence build completion and recover the image handoff

Status: Proposed
Date: 2026-09-05

## Context

Build workers can race direct notifications with fairness polling. Previously,
fairness selection did not lock the queued build. Source uploads happened after
queue publication. Success then wrote artifact metadata, notified imaged, wrote
provenance, and marked the build succeeded independently. Cancellation, reaping,
a database error, or a lost notification could leave contradictory state.

## Decision

- Fairness selection ranks quiet accounts first, then FIFO, and locks only the
  selected build using `FOR UPDATE OF b SKIP LOCKED`. The outer update also
  requires `queued` status.
- Reserve the source build ID and upload its archive before creating durable
  deployment work. Publish the build row and deployment's `building` state in
  one transaction that serializes with cancellation. On enqueue errors, mark
  only an unqueued, nonterminal deployment failed; an uncertain successful
  commit or a concurrent cancellation must survive that compensation.
- Use the original `(build ID, deployment ID, started_at)` claim as the ownership
  token. Complete or fail only a matching running claim and an eligible
  deployment. Success commits artifact metadata, provenance, and build status
  together. Failure commits both terminal states together. Lock deployment
  before build, matching deployment cancellation.
- Notify imaged only after success commits. At startup and every two seconds,
  imaged also finds succeeded builds whose deployments still await imaging.
  Process these through the existing handler, in batches of 16, serialized
  with normal notifications. Match the existing node routing contract:
  configured nodes ignore foreign owners; empty identities retain single-box
  and rolling-upgrade compatibility.
- Preserve an existing SBOM key when provenance is upserted without one.
- Stop builder children through `StopInstance` without starting a second
  destroy/export operation. While an original destroy owns cleanup, its export
  registration remains available to locate the child. Poll the durable claim
  during VM execution to recover missed cancellation notifications. A cancelled
  destroy context also kills the child, and export errors still release local
  resources.
- Verify cached artifact bytes against a separate SHA-256 digest. Missing,
  empty, or corrupt entries miss and can be repaired. Old entries without an
  artifact digest miss once. Preserve source executable bits and skip symlinks
  when granting the unprivileged build user access.
- Identify deployment cache entries with a versioned build recipe containing
  the full archive SHA-256, normalized source root, framework, plan, and runtime
  base reference. Workspace members sharing an archive must not share output
  unless their selected roots also match. Empty and `.` roots are equivalent.
  Never fall back to unversioned entries: their producing root is unknown.

## Consequences and limits

No schema or protobuf change is required. Roll out vmmd with builderd so the
new stop behavior is available; older vmmd versions do not implement builder
interruption during export. Checksumming cache hits adds an artifact read.
The recipe namespace causes a one-time cache miss for old entries; existing GC
continues to collect them. The source digest in provenance remains the archive
SHA-256, separate from the cache recipe digest. Changes to build semantics or
recipe encoding require a recipe version bump. Builder/toolchain digests and
platform partitioning remain follow-ups; the recipe does not yet promise full
build reproducibility.

Recovery covers the committed build-to-imaged handoff before imaging starts.
It does not resume an imaged process that crashes partway through conversion.
Uploaded but unqueued source objects and deployment rows left by an apid crash
still need retention/reconciliation. Cache GC leases, export retention, full
log delivery, toolchain identity, incremental caching, CI/release gates, and
release-tag ordering remain separate follow-ups to the build pipeline audit.
