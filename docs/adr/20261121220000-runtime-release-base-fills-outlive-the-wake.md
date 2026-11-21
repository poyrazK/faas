# ADR-20261121220000: Runtime-release base fills outlive the wake that started them

Date: 2026-11-21
Status: Accepted
Related: ADR-005 (cold boot must always work), ADR-054 (read-through node
cache), ADR-736 (immutable runtime releases), ADR-20261117190000 (compute
reaches the shared store only through signed grants)

## Context

A node's read-through cache (`storage.LocalCacheBackend`, ADR-054 §2) used
to fill a miss under the caller's context. A cold boot's wake deadline is
about 27.5 s. On GCS a whole runtime-release base arrived well inside it, so
this never mattered.

Production moved to OVH bare metal on 2026-11-20. OVH S3
(`s3.bhs.io.cloud.ovh.net`) serves 12–19 MB/s per stream, so a 449 MB
`base/releases/runner-node22-amd64-<id>.ext4` takes 23–37 s. During the
rc.364 rollout (2026-11-21), every cold boot of a function on the new base
on bhs-3 cancelled its own fill at the wake deadline
(`parent read: context canceled`), and every retry started the download
over. Deploys failed with `cold_boot_timeout` / `app_startup_timeout`. Only
the node whose imaged built the base (`pkg/imaged/runtime_release.go`) had
it cached. The operator installed the base on bhs-3 by hand. Every later
runtime release would fail the same way on every node that did not build
it, which breaks ADR-005: the first cold boot never succeeds.

## Decision

A cache miss of an immutable runtime-release base
(`base/releases/runner-*.ext4`, the key `state.RuntimeRelease.BaseKey`
produces) is filled in the background, once per key per process:

- The fill reads the parent under `context.WithoutCancel(caller)`. It keeps
  the caller's values but not its deadline or cancellation, and it has its
  own bound of 10 minutes (`storage.DefaultDetachedFillTimeout`).
- The fill always installs a durable cache entry (fsync, rename, sidecar),
  as a non-deferred miss does today.
- Callers wait for the fill or their own context, whichever ends first. A
  caller whose deadline passes returns its context error, and the fill goes
  on. The retry joins the in-flight fill or hits the installed entry.
  Concurrent wakes of the same base share one download.
- A failed or timed-out fill is not remembered: the next miss starts a new
  one. Stale fallback (`FAAS_STORAGE_CACHE_SERVE_STALE`) applies as before.
- `Refresh` (release adoption's integrity read) and every other key keep the
  attached fill. Snapshots, app layers and mutable logical bases are out of
  scope.

Only immutable keys qualify. ADR-736 never rewrites a release's bytes under
its key, so a fill that no caller waits for any more can never install
stale content, and a `Delete` racing a fill cannot resurrect a changed
object.

This is the smallest change that restores ADR-005 on slow stores. The
second or third wake of a new base succeeds on every node, and the platform
retries a cold boot after `cold_boot_timeout` anyway.

### Alternatives not taken

- **Prefetch new `runtime_releases` rows on every compute node.** This makes
  even the first wake fast, but it needs a new control-plane-to-node
  notification path, or polling, plus per-node retention decisions. It can
  be added later on top of this change, and a prefetch would then simply be
  a detached fill.
- **Parallel ranged GETs for large objects.** These make the download faster
  but leave it bound to the wake. A 2 GB base, or a slower store, would hit
  the same failure.

## Consequences

- The first wake of a new base on a node that does not have it can still
  time out. Its fill completes in the background, and a later wake
  succeeds. Deploy acceptance on a fresh release should expect one retry,
  not a failure.
- Up to one background download per distinct base key per process. Bases
  are few (six families × architectures per release), so this needs no
  extra concurrency cap.
- A process exit stops an in-flight fill, and its temp file is removed as
  before. The next miss starts again.
- Logs: `storage cache: caller stopped waiting; fill continues in the
  background` when a wake gives up, then `background fill installed` or
  `background fill failed` with `duration_ms`.

## Validation

`pkg/storage/cache_detached_fill_test.go` checks the following. After a
cancelled wake, a retry gets a cache hit or joins the fill, with one parent
read. A mutable key stays attached. Concurrent wakes share one fill. A
failed fill is retried. A hung store is cut off by the fill timeout. With
the fix disabled, the cancelled-wake cases fail.

Metal validation is owed: deploy a function on a newly published runtime
release on a compute node that did not build it (OVH, S3 parent), and check
that a later wake succeeds without hand-installing the base and that the
cache holds one `.meta`-backed entry for the base key.
