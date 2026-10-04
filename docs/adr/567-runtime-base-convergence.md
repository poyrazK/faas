# ADR-567 · Keep every node's runtime base byte-identical to its publication

- **Status:** proposed
- **Date:** 2026-10-04
- **Numbering:** renumbered from ADR-531 because the S3 multipart transfer
  decision already occupies that number on `main`.
- **Amends:** ADR-510 (snapshot backing image identity); ADR-053 (parent-ref
  base staging). ADR-005's cold-boot fallback stays the recovery path.
- **Decision:** imaged records the SHA-256 and size of the exact bytes it
  publishes under a runtime base key in a `<key>.content` sidecar. Every
  imaged, every minute and at start-up, re-reads the base's `.digest` and
  `.content` sidecars from the canonical parent (not its cache). When the
  publication was built from the same immutable source ref with this
  daemon's guest-init, and the node's cached copy has different bytes,
  imaged refreshes the cached copy from the parent. A publication without a
  `.content` sidecar (published before this ADR) is adopted once and its
  identity recorded. A sidecar that still disagrees with the parent after a
  refresh (a publisher between its two writes) is not retried until the
  sidecar changes.
- **Why:** ADR-510 restores a snapshot only onto the base bytes it was
  captured with, and its identity is content, so a capture taken on one node
  restores on another only when both hold the published bytes. They did not.
  ext4 builds (`mkfs.ext4 -d`) are not byte-deterministic, and imaged reads
  the publication's `.digest` sidecar through its own cache. After a release
  changes guest-init, every node sees its stale cached sidecar, rebuilds the
  base and republishes it. Each node keeps its own build; the parent holds
  whichever node wrote last. On production-us on 2026-10-04 the two compute
  nodes held different `runner-node22` (4c50229b vs 7a65496c) and
  `runner-python312` (c88ce5a7 vs 6c9d3890) bases with the same guest-init.
  Over 6 hours, vmmd refused 68 cross-node restores with
  `snapshot backing images changed`; each wake cold-booted (p50 ~3 s,
  p95 ~13 s, against ~0.5 s restores) and recaptured a snapshot that the
  other node then refused, so apps alternating between nodes never restored.
- **Consequences:**
  - Within a minute of any publication, every node attaches the published
    bytes, and new captures restore on any node. Snapshots captured against
    a node's old copy are refused once after it converges (ADR-510), so each
    app pays one cold boot per convergence.
  - The guest-init gate means a node never adopts a base whose PID 1 is from
    another release. During a rolling rollout, nodes converge per release.
  - A divergent base costs one ~250 MiB download per node. A steady state
    costs two small sidecar reads per staged base per minute and one stat;
    local digests are memoized per (device, inode, size).
  - Running VMs keep the inode they opened; a refresh renames a new file.
  - Uncached storage routes (single-box local storage) have no local copy to
    drift and are skipped.
- **Rejected alternatives:**
  - Deterministic ext4 builds (fixed UUID, hash seed, timestamps): directory
    population order and allocation still depend on the host, and any future
    mkfs change would silently break identity again.
  - Converge in vmmd: vmmd attaches the base but does not know which
    guest-init a publication carries, so it could hand an old daemon a newer
    PID 1 mid-rollout.
  - Read the publication sidecars fresh on imaged's staging path so later
    nodes skip the rebuild: still needed as a follow-up to avoid redundant
    builds, but it does not repair divergence that already exists, and a
    node that skips must still replace its stale cached copy, which this ADR
    provides.
