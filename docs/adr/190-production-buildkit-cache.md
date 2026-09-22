# ADR-190 · Production BuildKit dependency cache

- **Status:** accepted
- **Date:** 2026-09-20
- **Decision:** reuse app-scoped BuildKit cache records across production
  source deploys, for both Railpack and Dockerfile builds.
- **Why:** production Dockerfile deploys were rebuilding unchanged dependency
  and toolchain layers inside every ephemeral builder VM. A measured small Go
  image spent about 144 seconds in the image-build stage.
- **Consequences:** source edits can reuse matching BuildKit records while
  exact-source build artifacts, builder isolation, and cold-build fallback are
  unchanged. First builds and pull-request previews remain cold.
- **Rejected alternatives:** larger builder VMs increase the beta fleet cost;
  retaining builder VMs weakens the ephemeral-builder boundary; a shared
  cross-app cache would create an unnecessary tenant-isolation risk.

## Context

ADR-153 introduced a tenant- and app-scoped local BuildKit cache for
`gregale dev`, and ADR-159 extended its eligibility to Dockerfile projects.
The host-side cache is already bounded, expires after 48 hours, rejects
symlinks and oversized exports, and degrades to a cold build on every cache
error. Production deploys did not opt into it, even though they use the same
ephemeral builder path and benefit from the same instruction-level
invalidation.

The Dockerfile guest command also did not actually pass the cache importer and
exporter flags to `buildctl`. Builderd staged and collected the cache, but the
build itself ignored it. This decision closes both gaps.

## Decision

Production and developer-session builds derive the dependency cache key from
the account ID, stable app ID, selected source root, framework, and immutable
runtime base. The app ID prevents reuse across two applications owned by the
same account. BuildKit remains responsible for validating Dockerfile
instructions, source files, lockfiles, and base-image inputs before reusing an
individual record.

Both the Railpack and Dockerfile `buildctl` invocations import a staged cache
when present and export a replacement cache after a successful solve. Cache
publication stays advisory: a missing, corrupt, oversized, or unpublishable
cache never fails a valid application build.

Pull-request preview builds remain ineligible in this slice. Their short-lived
app identities would occupy the node-local budget while providing little
repeat-build value. The cache remains node-local, so a two-node fleet may pay
one cold build per node before subsequent edits consistently hit.

## Validation

Pure tests pin production eligibility, account/app/workspace/framework/runtime
partitioning, pull-request-preview exclusion, and cache flags on both Railpack
and Dockerfile `buildctl` commands. Existing builder cache validation and metal
acceptance tests remain authoritative for filesystem safety and real layer
reuse.
