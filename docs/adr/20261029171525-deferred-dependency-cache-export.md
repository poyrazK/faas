# ADR-20261029171525 · Export the dependency cache after the image handoff

- **Status:** Proposed
- **Date:** 2026-10-29
- **Amends:** when ADR-190's production BuildKit dependency cache is exported.
  What the cache holds, its keys and its import are unchanged. ADR-003, under
  which builds run only inside builder microVMs, is unchanged.
- **Switch:** builderd `FAAS_BUILD_DEFER_CACHE_EXPORT`, default off.

## Context

A build with an app dependency cache runs one `buildctl` solve with two
exporters: the OCI image (`--output`) and the cache
(`--export-cache type=local,mode=max`). BuildKit runs them one after the
other, and builderd sees neither until the VM powers off.

Production on rc.304, 2026-10-29, cold Node build:

| Step | Time |
|---|---|
| Export the OCI image | 12.5 s |
| Export the dependency cache | ~115 s (106 s "preparing build cache for export") |

The deploy needs only the image. The cache only speeds up the next build of
the same app. Yet every cold deploy that carries a dependency cache waits
about two minutes for it before imaged can scan and snapshot.

ADR-190 Amendment 1 skips the export when the plan and install inputs match
the imported cache. It does not help a build whose dependencies changed, or
an app's first build.

One rejected option was to hand the image over early through a vsock from the
running VM. Customer build steps run inside that VM, and a vsock is not
namespaced inside it, so a build step could have spoken for guest-init. It
would also have needed a new data path in root vmmd.

## Decision

With the switch on, a build that has a dependency cache key is split across
two VMs on one drive.

1. **The build VM** gets `defer_cache_export` in its manifest. It exports the
   image and no cache. Railpack's inputs check still runs. It either keeps the
   imported cache, as today, or records the digest of the inputs that the owed
   export covers. guest-init reports `dependency_cache_deferred` in
   build-done.
2. **builderd** reads the result. It keeps the build drive instead of deleting
   it, and hands the image to imaged at the usual point (`completeBuild`).
3. **The cache export VM**: builderd writes a `cache_export_only` manifest and
   a fresh entropy seed into the offline drive with debugfs, and drops the old
   build result. These are the same edits the warm-builder refresh makes. It
   then cold-boots `build-<id>-cache` on that drive.
   - guest-init does not unpack the source again.
   - It empties `/build/out` except the inputs digest.
   - It restarts BuildKit on the root the build left on the drive.
   - It solves the build's own plan with only `--export-cache`.
   - Every step hits the BuildKit state from the build, so the run's time is
     the export the build skipped.
4. **builderd** publishes the cache as before, and then deletes the drive and
   the second export directory.

The builder slot stays held until the second VM ends. A cache export
therefore never shares the builder with the next build: the slot rule
(builds never outrank tenant wakes) and the builder memory budget are
unchanged. The second VM runs under `BuildCacheExportTimeoutSeconds`
(600 s). It is registered for drain like a build VM, so drain stops it.

**Which builds defer.** Only builds with a dependency cache key, and only
when none of the following applies:

- **Warm builder:** its drive is captured for its own snapshot.
- **Build secrets or registry credentials:** the second VM gets neither, so a
  Dockerfile solve could not resolve a private base image or mount a secret.
- **Dockerfile build with selected build dependencies:** it runs with
  `--no-cache`, so a second solve would rebuild every step.

These builds export inline as today.

**Failure handling.** The cache is a cache, so a failure of the second VM
costs only the next build's cache:

- builderd logs it, and the deployment is unaffected.
- On every path where the second VM does not run, builderd deletes the kept
  drive. This covers a build that fails or is cancelled after its VM exits,
  a failed handoff, and drain.
- The second VM publishes only a `/build/out/cache` it wrote itself. A VM
  that dies early therefore leaves the stored cache as it was.

**Compatibility.** Both manifest keys and the build-done key are `omitempty`.

- An older guest ignores `defer_cache_export` and exports inline. It never
  reports the export deferred, so builderd deletes the drive as before.
- builderd keeps the drive only if it asked for the deferral and the guest
  reports it.

**Security.** The second VM is the same tenant's builder, on a drive only
that tenant's build wrote. It runs in its own jail under the same cgroup
slice, network policy and plan as the build. It gets fewer inputs than the build: no source
archive, secrets or registry credentials. builderd edits the offline drive
with debugfs as its non-root user, as the warm-builder refresh already does.

## Rollout

1. Release the guest and builderd change everywhere. Nothing changes yet.
2. Turn the switch on for one node's builderd with a systemd drop-in.
   Deploy the benchmark app with changed dependencies, then compare it with
   the other node on two measures: time from build start to imaged handoff,
   and the logged `deferred dependency cache saved` duration. Then confirm
   that the next build restores the cache.
3. If it holds, make the switch the default in a follow-up.

## Consequences

- Expected: about 115 s less to a cold deploy's image handoff whenever the
  build exports a dependency cache.
- The builder slot is held longer per such build. It covers the second VM's
  boot, BuildKit start and fully cached solve, on top of the same export.
  The next queued build starts that much later. This is measured in rollout
  step 2.
- The build drive lives for the extra minutes on the builder disk. The
  janitor still removes any drive older than an hour that a crash left
  behind.
- Not covered: a faster cache export itself. Kernel overlayfs
  (ADR-20261029105325) and zstd layers (ADR-20261028084018) work on that.
