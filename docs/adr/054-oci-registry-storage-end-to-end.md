# ADR-054 · `OCIRegistryStorageBackend` end-to-end

- **Status:** accepted (revised 2026-08-07)
- **Date:** 2026-07-31
- **Issue:** #95 slice 4 (multi-box rollout, Tier 1 Phase 3)
- **Decision:** Wire `pkg/storage.OCIRegistryStorageBackend` into every
  production code path that today reads or writes a per-app layer or
  snapshot through local-disk hardcoded paths. The driver already exists
  (PR #410's `pkg/storage/oci.go`), the `PrefixRouter` already exists
  (PR #410's `pkg/storage/router.go`), and `BackendFromEnv` already
  routes `/var/lib/faas/apps/` to a sibling backend and `/srv/fc/{snap,
  base,kernel,layers}` to the local backend. What's missing is
  production wiring: today's `pkg/fcvm/manager.go`, `pkg/sched/disk_drift.go`,
  and `pkg/imaged/{handler,gc}.go` still call host-path helpers directly,
  so `FAAS_STORAGE_BACKEND=oci` is a configuration knob that has zero
  effect on the hot path.

  This ADR commits to:

  1. **Routing rule stays at `BackendFromEnv`.** `/var/lib/faas/apps/`
      keys (per-app layers, snapshots, app-scoped blobs) route to the
      OCI backend when `FAAS_STORAGE_BACKEND=oci`; `/srv/fc/{snap,
      base,kernel,layers}/` keys stay local (the canonical content-
      addressed paths; latency-sensitive on every cold boot; small
      enough to keep on every box). The local prefix set is
      configurable via a new env var `FAAS_STORAGE_LOCAL_PREFIXES`
      (comma-separated, defaults to `/srv/fc/base,/srv/fc/snap,
      /srv/fc/kernel,/srv/fc/layers`). The router accepts this
      extension cleanly because the `PrefixRouter` already supports
      arbitrary prefix→backend maps.

  2. **`LocalCacheBackend` (read-through cache).** A new
      `pkg/storage/cache.go` wraps any `StorageBackend` with an LRU
      on disk rooted at `FAAS_STORAGE_CACHE_DIR` (default
      `/var/lib/faas/cache`). Put writes through to the parent +
      caches; Get reads from cache first, falls back to parent +
      populates. The cache survives a registry outage: a stale
      entry is better than a cold-boot fail (issue #96 review
      finding). Wire it in `BackendFromEnv` when
      `FAAS_STORAGE_CACHE_DIR` is set; production today has it
      unset (single-box doesn't need it).

  3. **Production code paths go through `StorageBackend`.**
      `pkg/fcvm/vmm.go::materializeFromStorage` already takes a
      backend; the audit here is to confirm no remaining host-
      path literal (`/srv/fc`, `/var/lib/faas/apps`) in the call
      graph below `WithStorage`. The two known offenders today:

      - `pkg/fcvm/manager.go::restoreSnapshot` and `::parkInstance`
        read/write snapshots under `/srv/fc/snap/<dep>/`. They go
        through the storage backend (key `snap/<dep>/{mem,vmstate}`).
        Local-only keys — never hit OCI.
      - `pkg/sched/disk_drift.go` enumerates the apps directory
        via `os.ReadDir(<appsRootPath>)` directly. Replace with
        `storage.LocalArtifactLister.List("apps/")` (already
        implemented; just stop reading `appsRootPath` outside the
        backend's wiring). This is the most invasive diff in the
        slice; keep it small.

  4. **`pkg/imaged/handler.go` writes through `StorageBackend`.**
      Today's `SetDeploymentRootfs` opens `/var/lib/faas/apps/<slug>/<dep>.ext4`
      directly. Replace with `backend.Put("apps/<slug>/<dep>.ext4",
      ext4Reader)`. The DB row contract (`apps.rootfs_path` /
      `deployments.rootfs_path`) is preserved as a derived cache
      key for diagnostics, but the actual blob write goes through
      the backend. `pkg/imaged/gc.go::GCUnreferencedApps` similarly
      routes List/Delete through the backend.

  5. **`cmd/{imaged,vmmd}/main.go` reads `FAAS_OCI_*` creds at
      startup.** The OCIRegistryStorageBackend is constructed
      exactly once per daemon via `BackendFromEnv` and threaded
      through the existing wiring. No new env vars in this slice
      beyond `FAAS_STORAGE_CACHE_DIR` and `FAAS_STORAGE_LOCAL_PREFIXES`.

  6. **`LocalArtifactLister` capability detection on the router.**
      The router already supports this (PR #410): if every backend
      implements `LocalArtifactLister`, the router aggregates List
      calls. The new `LocalCacheBackend` implements it too. The
      audit here is "no remaining `os.ReadDir` in production code
      paths that should be routing through the backend".

- **Why:** Today, every compute node on a multi-box fleet must hold
  a local copy of every app's per-app layer. The §4.6 two-drive
  storage economics (shared base + per-app layer) collapse to
  "every box is fat": a Scale app with a 2 GB layer needs 2 GB
  per box, times N boxes. Per-app layer fetches on cold boot add
  a single round-trip latency cost (the spec §4.5 ≤ 350 ms cold
  boot target absorbs it). Without this ADR, the multi-host
  runbook (`docs/runbooks/multi-host-rollout.md`, issue #297
  Phase D / PR #453) carries the Pre-conditions callout
  *"Phase 3 must ship before this runbook is production-safe"*.

- **Consequences:**
  - **Stateless compute nodes.** After this slice, compute nodes
    boot with `/srv/fc/{base,snap,kernel,layers}` populated
    (one-time imaged step at provisioning) and the per-app layer
    fetched on demand. Adding a box no longer requires an rsync
    of `/var/lib/faas/apps` from a peer.
  - **Per-app layer blob storage moves to a registry.** Operators
    pick a registry (Hetzner Container Registry, GHCR, self-hosted
    distribution) at deploy time; the same registry serves every
    compute node. The `FAAS_OCI_*` env vars and the
    `cmd/gregalectl pki init`-style bootstrap story extend naturally.
  - **Registry outage degrades cold boot.** The
    `LocalCacheBackend` keeps last-known-good blobs on disk; a
    registry outage means stale layers are served (which can still
    cold-boot the right app version) until the cache ages out.
    Cache TTL is per-blob based on the registry's manifest
    `Last-Modified`; a v1.1 ADR tightens this.
  - **GC story.** imaged's `GCUnreferencedApps` runs against the
    registry today; `pkg/sched/disk_drift.go`'s local-only GC goes
    away. The OCI backend's `List` walks each repo's tags via
    `knownRepos` (PR #410); a cold-start registry with no
    `/v2/_catalog` support falls back to the daemon-local
    `knownRepos` cache, which is warmed by the daemon's own Put
    history.
  - **Egress policy.** OCI pull uses
    `oci.NewEgressHTTPClient()` (PR #410), which refuses
    RFC1918 + link-local + metadata ranges — the same posture
    as build-time image pulls. A misconfigured `FAAS_OCI_REGISTRY`
    pointing at a private host fails fast at startup.

## Reference call sites

| Component | File | Change |
|-----------|------|--------|
| router | `pkg/storage/env.go::BackendFromEnv` | accept `FAAS_STORAGE_LOCAL_PREFIXES` (default `/srv/fc/base,/srv/fc/snap,/srv/fc/kernel,/srv/fc/layers`); construct `PrefixRouter` from the configured prefixes + apps prefix |
| cache | `pkg/storage/cache.go` (new) | `LocalCacheBackend` LRU on disk; implements `StorageBackend` + `LocalArtifactLister` |
| cache | `pkg/storage/cache_test.go` (new) | LRU eviction, registry-unreachable serves last-known-good |
| fcvm | `pkg/fcvm/manager.go` | replace direct `/srv/fc/snap/...` reads with `backend.Get("snap/<dep>/{mem,vmstate}")` |
| sched | `pkg/sched/disk_drift.go` | `Tick` enumerates `snap/<depID>/` via `backend.List("snap/")` when a `LocalArtifactLister`-capable backend is wired via `WithStorage`; falls back to `os.ReadDir(SnapDir())` when storage is nil or the backend List errors (intentional fail-soft, pinned by `TestDiskDrift_StorageBackend_ListErrorFallsBackToDisk`) |
| imaged | `pkg/imaged/handler.go` | `SetDeploymentRootfs` writes via `backend.Put("apps/<slug>/<dep>.ext4", ext4)` (path stored in the diagnostic `rootfs_path` DB column); the per-deploy grype scan source routes through `stageScanExt4` (ADR-054 acceptance closure, Tier 1 Phase 3) so OCI-mode deploys don't silently lose the §17 G1 scan guarantee |
| imaged | `pkg/imaged/loop.go` + `pkg/imaged/handler.go` (`cleanupDeployFiles` / `cleanupAppFiles`) | `deleteSnapshotsAndFiles` + `cleanupDeployFiles` + `cleanupAppFiles` route List + Delete through the wired `StorageBackend` (per-route keys for snap mem/vmstate; canonical `sched.AppLayerKey(slug, dep)` for the per-app ext4). Pure-function retention algorithm stays in `pkg/imaged/gc.go` |
| vmmd | `cmd/vmmd/main.go` | load OCI creds + construct backend once at startup |
| imaged | `cmd/imaged/main.go` | load OCI creds + construct backend once at startup |
| schedd | `cmd/schedd/main.go` | load backend once at startup (disk-drift + cosign verifier + `cosign.NewLocalVerifier` all consume the same `BackendFromEnv` result); the cache observer (`StorageCacheStaleFallback`) is attached here too |
| tests | `pkg/storage/cache_test.go` | new — LRU eviction, last-known-good fallback, opt-in stale fallback (`TestLocalCacheBackend_StaleFallbackEnabled` + `_StaleFallbackDisabled_DefaultContract`), fail-loud default (`TestLocalCacheBackend_ParentFailureSurfaces`) |
| tests | `pkg/storage/router_test.go` | extend — `TestPrefixRouterLocalPlusOCI` with apps→OCI, snap→local |
| tests | `pkg/imaged/handler_storage_env_test.go` + `pkg/imaged/handler_storage_routing_test.go` | `TestHandler_OCIBackendEnv_Handoff` pins env → Handler handoff (storage.BackendFromEnv returns *PrefixRouter, handler.storageFor() returns it unchanged); `TestHandler_StorageRouting_AppsToOCI` pins per-prefix dispatch to OCI vs local. Build-pipeline test delegation to these files (instead of `handler_image_build_test.go`) is intentional — the build pipeline cannot hermetically cover OCI mode without a live registry; see the existing doc comment at `handler_storage_env_test.go:48-55` |
| tests | `pkg/imaged/handler_storage_env_test.go` | `TestHandler_OCIBackendEnv_Handoff_ScanFromBackend` (added by ADR-054 acceptance closure) — wires an OCI-mode router via `WithStorage`, runs `runDeployScan` end-to-end against a stub `grypeRun` that records `<dir>`, asserts the recorded dir points at a `t.TempDir()`-equivalent stage (NOT the appsRoot path) |
| tests | `pkg/sched/disk_drift_test.go` | `TestDiskDrift_StorageBackend_*` (PresenceMatch, MissingFileIncrements, OrphanDepIncrements, ListErrorFallsBackToDisk) — pins the production `backend.List("snap/")` path |

## Out of scope

- **Multi-region replication.** The OCI backend today pulls from one
  registry; a v1.1 ADR adds `FAAS_OCI_REGISTRY_FAILOVER`. Not
  relevant to the Tier 1 Phase 3 slice.

## Amendment (2026-08-07)

Acceptance PR resolves three policy decisions made during review of
the production wiring, and downgrades the runbook `> [!CAUTION]`
block in `docs/runbooks/multi-host-rollout.md`. The behaviour
described above stands; the deliverable for the acceptance PR is
the operator-facing contract.

1. **Stale-fallback is opt-in, not default.** The
   `LocalCacheBackend` `Get` method preserves the pre-acceptance
   fail-loud contract (wrap parent error, do not serve stale) when
   `FAAS_STORAGE_CACHE_SERVE_STALE` is unset. Operators opt in by
   setting the env var on the daemon. The `StorageCacheStaleFallback`
   counter in `pkg/wire/metrics.go` (exposed via the §12 storage
   panel) lets ops alert on the "registry is down" rate. The
   pre-acceptance test `TestLocalCacheBackend_ParentFailureSurfaces`
   in `pkg/storage/cache_test.go` continues to pin the fail-loud
   contract; the new `TestLocalCacheBackend_StaleFallbackEnabled` +
   `TestLocalCacheBackend_StaleFallbackDisabled_DefaultContract`
   pair pin the env-toggled branch.

2. **Cache defaults to on for oci mode.** When
   `FAAS_STORAGE_BACKEND=oci` and `FAAS_STORAGE_CACHE_DIR` is unset,
   `storage.BackendFromEnv` defaults the cache to
   `/var/lib/faas/cache` with the 1 GiB budget
   (`DefaultCacheMaxBytes`). Single-box (`local`) keeps opt-in
   behaviour — zero diff for existing operators. Explicit
   `FAAS_STORAGE_CACHE_DIR=""` always disables (the `os.LookupEnv`
   distinction in `storage.resolveCacheDir` honours the
   unset-vs-empty difference). The default-on path is pinned
   hermetically in `pkg/storage/env_test.go` via the
   `TestResolveCacheDir_*` + `TestBackendFromEnv_*-Hermetic` matrix
   — the cache construction is exercised through a `t.TempDir()` so
   CI never creates `/var/lib/faas/cache` on a non-prod machine.

3. **Runbook downgrade is part of this PR.** The `> [!CAUTION]`
   block in `docs/runbooks/multi-host-rollout.md` is downgraded:
   Phase 2 (`node_signature` on CapacityReport) and Phase 3
   (`OCIRegistryStorageBackend`) entries flip from "✗ NOT shipped"
   to "✓ shipped". The "Phase 3 must ship before this runbook is
   production-safe" gate line is replaced with the remaining
   ship-blockers (ADR-056 off-host PG backup, Gate-B cross-box mTLS,
   active-passive HA ADR).

The four items deferred in "Out of scope" stay deferred: multi-
region failover, tiered GC across registry + cache, compression /
dedup, and the cache hit-ratio Prometheus metric. The §12 storage
panel renders hit/miss/stale as three counters; the hit/miss split
is a v1.1 tightening.
- **Tiered GC across registry + local cache.** Today's GC is
  per-backend. A future ADR adds a single `pkg/storage/gc.go` that
  orchestrates cross-backend eviction.
- **Compression / dedup on the local cache.** Storage is cheap; the
  v1 cache is a 1:1 mirror. A future slice compresses when the
  cache exceeds a disk-fraction watermark.
- **`pkg/storage` access logging.** Operators want a Prometheus
  metric for cache hit ratio. Out of scope here; lives in the
  observability pass.

## Amendment (2026-09-07): runtime-base generation consistency

Runtime bases remain under stable logical keys such as
`base/runner-go124-amd64.ext4`, while production changes their digest-pinned
OCI reference between releases. A cache-first peer could therefore retain the
old base, digest sidecar, and scan sidecar indefinitely after another imaged
node published their replacements. The first observed failure was a clean Go
1.24 release rejected on the SSD node because its cache still held the
previous base's CRITICAL finding after the HDD node staged the replacement.

`vmmd` now loads the same `/etc/faas/runtime-bases.env` contract as imaged and
builderd and binds each logical base key to its configured immutable OCI
reference. `LocalCacheBackend` stores a node-local generation marker only
after imaged publishes the base, digest sidecar, and scan sidecar. On a marker
mismatch, vmmd refreshes all three keys directly from the canonical parent,
checks that the refreshed scan names the configured reference, and only then
commits the new marker and continues admission. A failed or partially
published generation stays fail-closed.

The marker also pins each remote member of that verified three-key group in
the node cache. Budget enforcement evicts unpinned snapshots and layers first;
it does not discard a prepared runtime base merely because unrelated traffic
fills the shared cache. The runtime matrix uses stable logical keys, so the
pinned set is bounded and a new release replaces the previous generation at
those keys. A generation marker for a canonical local parent does not pin its
redundant cache copy because the parent itself already provides the local fast
path.

The normal restore path reads the small local marker and scan sidecar without
a registry round trip. Registry I/O occurs once per node and runtime
generation, so the consistency repair does not consume the 350 ms full-wake
latency budget after adoption.

## Rejected alternatives

- **One flat `OCIRegistryStorageBackend` for everything (no
  PrefixRouter).** Single namespace; every blob goes to the
  registry. Rejected: cold-boot latency for `/srv/fc/base` and
  `/srv/fc/kernel` is critical (every wake reads kernel + base
  ext4), and a registry round-trip on every wake would saturate
  the wake-path RPS budget. The §4.6 two-drive model (shared
  base + per-app layer) needs the local-prefix set to stay
  local.
- **Object-storage backend (S3, DO Spaces).** S3-compatible storage
  is cheaper than OCI registries but adds a per-blob
  authorization layer that we don't need. The OCI distribution
  spec's bearer-token dance is already battle-tested
  (PR #410 review), and most cloud registries already speak
  it. A future slice adds a `S3StorageBackend` if the
  economics demand it.
- **Don't cache.** A pure read-through to the registry on every
  cold boot. Rejected: a registry outage silently bricks every
  cold boot. The `LocalCacheBackend` is the "registry
  unreachable → last-known-good" defence.
- **Ship OCI as a daemon-only driver (no router).** Every
  daemon calls `BackendFromEnv` directly; the router lives in
  the daemon. Rejected: the router is already proven (PR #410)
  and the per-daemon wiring of `apps` vs `snap`/`base` is
  identical across imaged / vmmd / schedd. The env contract
  stays in `BackendFromEnv`; daemons don't see the routing
  details.
