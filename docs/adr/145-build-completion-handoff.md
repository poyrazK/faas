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
- Partition cache entries by the staged builder base identity and target
  platform. Hash the builder base digest sidecar so the OCI config, base layout,
  and injected guest-init contract all participate without reading the full
  ext4 for every build. If the sidecar is missing, malformed, older than the
  base, or changes during a build, continue the build but do not reuse or
  publish a cache entry.
- On a cache hit, atomically hard-link the validated artifact into a
  deployment-specific lease before publishing build success. Cache GC may
  evict the canonical entry without invalidating this handoff. Imaged removes
  the lease after it replaces `rootfs_path` with the final app layer or records
  a terminal failure. Daily maintenance preserves leases still referenced by
  a deployment and removes crash leftovers after the reference changes.
- Refresh an entry's directory timestamp on every validated hit and evict by
  that access timestamp, so frequently reused artifacts remain in the cache.
  Serialize GC against lookup, lease publication, and cache writes within the
  builderd process.

## Consequences and limits

No schema or protobuf change is required. Roll out vmmd with builderd so the
new stop behavior is available; older vmmd versions do not implement builder
interruption during export. Checksumming cache hits adds an artifact read.
The recipe namespace causes a one-time cache miss for old entries; existing GC
continues to collect them. The source digest in provenance remains the archive
SHA-256, separate from the cache recipe digest. Changes to build semantics or
recipe encoding require a recipe version bump. The v2 recipe closes builder
toolchain and platform reuse, but external package registries can still make a
fresh build non-reproducible without dependency lockfiles.

Recovery covers the committed build-to-imaged handoff before imaging starts,
including cache hits whose canonical cache entry is evicted. It does not resume
an imaged process that crashes partway through conversion.

Builderd now runs a daily source-object sweep when split-box storage is enabled.
It enumerates the authoritative `sources/` namespace, derives the creation time
from the UUIDv7 build ID, preserves queued and running builds, and removes
terminal or unqueued objects after the configured 24-hour default retention.
The apid upload still precedes `CreateBuildWithID`, so an apid crash can leave
an orphan; the UUID age fence makes that orphan eligible without deleting a
newly uploaded object. Unknown or legacy non-UUIDv7 source names are retained
for manual inspection. The read-through storage cache delegates List to its
parent when available so the sweep sees remote objects that are not warm on the
builder node.

Export retention, full log delivery, incremental caching, CI/release gates, and
release-tag ordering remain separate follow-ups to the build pipeline audit.
