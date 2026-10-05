# ADR-586 — Durable regional build exports

## Status

Accepted for internal regional compute. Native Linux x86_64/KVM qualification
remains required before rollout.

## Context

ADR-598 moves source archives from apid to regional builders. A completed build
still handed imaged a producer-local absolute path, and recovery polling selected
only that producer. Losing the builder host after success could strand an otherwise
valid deployment. The runtime rootfs fields subsequently change during image
conversion, so they cannot also be the permanent reference to the builder export.

## Decision

Regional builders publish their OCI archive to the app's configured artifact
store before completing the build. The key is
`regions/<region>/build-exports/<publication-uuidv7>/<sha256>.oci.tar`.
Every publication has a fresh UUIDv7, independently of the build ID, so stale
attempts cannot replace committed bytes and orphan retention has a reliable
publication clock. Builderd checks the file type, plan cap, byte count and hash;
the upload must consume the same bytes that were hashed.

Deployments retain `build_artifact_key`, `build_artifact_sha256` and
`build_artifact_bytes` separately from the final runtime rootfs. State commits
this descriptor, build success and provenance in one transaction, under the
existing running-claim timestamp check. A stale or cancelled claim publishes
neither metadata nor success. PostgreSQL requires an export for new regional
build success, checks its shape and region, and prevents changes to a committed
descriptor or its app identity. Historical completed rows remain readable.

An active, named imaged worker registered in the app's region can materialize the
export. Producer identity remains the ownership rule for historical local
handoffs. Regional consumers fetch the persisted key without accessing the
producer's path, check routing, bound the download by the descriptor and plan,
and verify its exact size and SHA-256 before OCI parsing or rootfs conversion.
Function conversion consumes the dependency-complete OCI artifact too.

A deployment-scoped store lock serializes image materialization across workers.
Its lock namespace is separate from deployment activation. Compare-and-set
status advances prevent cancelled or failed work from being revived during a
download. Pending/building and unfinished imaging work can be converted again
from the immutable export after a process crash. Work already in security scan
uses its committed runtime layer; snapshotting work retries the scheduler
handoff. Stage projection remains best effort, and an imaging row whose stage
append was lost can safely repeat conversion. Fetch outages and failed
snapshot-prime notifications remain eligible for durable polling.

Runtime conversion, scanning, release tasks and snapshot priming keep their
existing component owners and two-drive behavior. This handoff does not add a
direct apid-to-vmmd path.

## Retention and storage

OCI storage maps exports to a fixed `build-exports` repository and a reversible
publication/digest tag. Fresh clients can inventory that repository without a
registry catalog. GCS and local drivers retain their existing opaque-key
behavior; the region router supplies the namespace boundary.

Builderd's existing source sweep cadence and maximum age also apply to remote
exports (24-hour defaults). Inventory spans all configured routes and deletion
preserves the complete regional key. Pending, building, imaging and snapshotting
deployment references protect exports. Unknown key shapes and fresh uploads are
retained. An incomplete inventory or reference lookup aborts planning before any
deletion. A succeeded build with a committed remote export releases its local
producer export, and regional cache-hit leases are released by the producer.

## Validation and rollout

Portable tests exercise publication failures, short uploads, stale/cancelled
completion, corruption and size mismatches, placement eligibility, incomplete
cleanup inventories, crash recovery and notification retry. A connected
builderd/imaged test removes the producer export and disables that node before
two regional peers race; one conversion produces the regional runtime layer for
both an app and a Go function. PostgreSQL tests cover completion rollback,
descriptor constraints, recovery selection and migration replay.

`TestNativeRegionalBuildExportAcceptance` is a guarded test for the dedicated
three-worker fleet described in ADR-592. It additionally requires
`FAAS_REGION_SOURCE_TARBALL`, a known-good HTTP app archive for that fleet. The
test pauses regional image consumers and the peer builder, uploads through apid,
captures the producer's successful export, stops its builderd/vmmd and removes
its admission, and starts the peer imaged. The peer must have no local copy of
the producer path. The test then verifies runtime publication and a cold HTTP
wake on the peer after invalidating snapshots. Cleanup restores services and
producer admission and deletes the test app.

Run `RUN_REGEX='^TestNativeRegionalBuildExportAcceptance$' RUN_ARGS='-timeout=30m' make test-metal`, then
`make leakcheck` on each affected host. Compilation and portable fixtures do not
qualify the native fleet behavior.

Drain outstanding regional builds before upgrading. Install the migration and
upgrade builderd and imaged together, with the same artifact routing file and
registered compute-node names. Retention workers need every participating
route. Rollback refuses to discard existing regional export references. Keep
regional compute internal/default-off until dedicated native regional acceptance,
affected `test-metal` coverage and `leakcheck` pass.

Logs/SBOM storage, persistent workloads, regional ingress and control-plane
independence still require implementation or qualification; this ADR completes
the source-build export handoff, not the full multi-region rollout.
