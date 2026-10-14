# ADR-153 · Developer BuildKit dependency cache

- **Status:** accepted
- **Date:** 2026-09-05
- **Decision:** reuse tenant-scoped BuildKit cache metadata across ephemeral developer builder VMs; keep production and Dockerfile builds unchanged.

## Context

`gregale dev` deliberately uses the production upload, build, image and
Firecracker paths. That gives developers strong parity, but a source-only edit
currently starts BuildKit with an empty worker root and reinstalls unchanged
dependencies. Keeping the builder VM alive would make that VM a new long-lived
tenant resource and weaken the existing ephemeral-builder boundary.

## Decision

Railpack builds for apps marked as developer sessions export a BuildKit local
cache after a successful build. `vmmd` copies it out with the existing build
artifacts; `builderd` publishes it atomically below its builder-drive directory.
The next build for the same account, developer app, workspace root, framework
and runtime base copies that cache into the fresh drive before boot.

The outer SHA-256 key prevents cache reuse across tenants, developer
workspaces, selected monorepo members, frameworks or runtime bases. BuildKit's
content-addressed cache records remain the authority within that partition, so
changed lockfiles and build inputs invalidate their own layers. Raw key inputs
never become path components.

Builder VMs remain ephemeral, use a fresh drive and entropy seed, and retain
the same cgroup, jailer and network boundaries. The cache is data, not an
executable VM snapshot. Symlinks and non-regular cache entries are rejected;
copy size is capped by the existing maximum exported-layer ceiling. Cache
restore and publication failures fall back to a cold build and never fail a
valid deployment. Entries expire after 48 hours without a successful refresh;
oldest entries are also evicted above a 16 GiB aggregate internal cache budget.

Dockerfile builds are excluded from this first slice because arbitrary cache
mount and secret behavior needs separate qualification. Production and pull
request preview builds do not read or write this cache.

## Consequences

Source-only `gregale dev` rebuilds can reuse package-install and toolchain
layers without sharing a VM or filesystem between customers. The CLI streams a
clear cold/restored cache line and prints total sync-to-live duration. Builds
still upload a complete source archive; incremental transfer is a separate
follow-up.

## Validation

Pure tests pin tenant/workspace/runtime key isolation, atomic cache publication,
size and symlink rejection, stale-cache cleanup, developer-only eligibility and
the BuildKit import/export argv. The existing metal acceptance remains the
authority for Firecracker boot and artifact export behavior.

## Amendment 1 (2026-10-14): keep an unchanged cache instead of re-exporting it

Every build that uses the dependency cache (this ADR and ADR-190) exported it
again with `mode=max`, base-image layers included, even when nothing it holds
could have changed. On the internal test node that export took 455 s of an
888 s developer build, more than the build itself.

After `railpack prepare`, the build shell runs a re-executed guest-init that
hashes the Railpack frontend, the plan and the plan's install inputs (the
manifests, lockfiles and config the install steps copy in, plus
`railpack.json`, `gregale.yaml` and `.gregaleignore`). The digest travels in
the exported cache next to `index.json`. When the digest of a new build
matches the one in the imported cache, buildctl runs without `--export-cache`,
`build-done.json` reports `dependency_cache_unchanged`, and builderd keeps the
stored cache and refreshes its age instead of publishing. Any other answer,
including a hashing error, exports as before. BuildKit still validates every
imported layer, so a wrong answer can cost a cache miss, never a wrong image.
Dockerfile builds keep exporting every time: their install inputs are not
known before the build.

Metal (internal test node, 2026-10-14): in `TestDevLoopMetal` the first
developer build exported the cache (the export step took 171 s; builder VM
295 s), and the second, a source-only edit, logged `dependency inputs
unchanged; keeping the imported dependency cache` and its builder VM
finished in 122 s. `TestDevWatchMetal`'s second build kept the cache too.
