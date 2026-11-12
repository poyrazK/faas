# ADR-20261030122204 · Builder VMs start from a seeded BuildKit root

- **Status:** Accepted (2026-11-12). The decision gate is met; see "Gate
  result".
- **Date:** 2026-10-30
- **Amends:** the builder VM's drive layout (spec §4.6 describes drive0 and
  drive1; builders gain an optional drive2). ADR-003, under which builds run
  only inside builder microVMs, is unchanged. So is
  ADR-20261025090100: the host never mounts a guest drive.
- **Switch:** builderd `FAAS_BUILD_BUILDKIT_SEED`, default off.

## Context

Every Railpack build starts BuildKit on an empty root on a fresh drive1. Its
first step pulls and unpacks the Railpack builder image
(`ghcr.io/railwayapp/railpack-builder:mise-<version>`: 8 layers, 419 MB
compressed, the largest 240 MB). That happens for every Railpack build of
every app, cold or not, and no current mechanism avoids it:

| Measured | Builder image step |
|---|---|
| Production, rc.304, 2026-10-29, cold Node build | 60–64 s, of which about 54 s unpacks |
| Acceptance host, 2026-10-30, from the build step timings (#5945) | 70–101 s, the slowest step of every build |

The time goes to unpacking, not downloading. The npm/PyPI package cache
investigation (2026-10-29) found downloads are 1–3% of the build before
export, and the image download is a few seconds of that step.

What does not avoid it:

- **The app's dependency cache** (ADR-190) imports BuildKit cache records
  for the app's own steps. The builder image is a `docker-image://` source,
  not a cached step, and BuildKit still unpacks it.
- **Warm builders** keep one app's BuildKit state in a paused VM. They are
  off in production because the restored guest kernel keeps a stale view of
  the drive that builderd edited (2026-10-04). They are also per app, so
  they never help an app's first build.
- **A registry mirror or pull-through cache** saves only the download.
- **Kernel overlayfs** (ADR-20261029105325, `FAAS_BUILD_KERNEL_OVERLAY`) may
  make unpacking itself much cheaper. Its production A/B has not run yet.

Production compute nodes keep build drives on `/srv/fc`. That is XFS with
`reflink=1` (checked on gregale-compute-1, 2026-10-30), so a copy of a file
there shares its blocks until either copy writes them.

## Decision

builderd keeps a **BuildKit seed drive** per node and gives every Railpack
build a reflink copy of it as a writable drive2 that holds the BuildKit root.

1. **The seed run.** A builder VM boots with a platform-only manifest
   (`buildkit_seed_only`): no app source, no secrets, no registry
   credentials, and no tenant. It starts BuildKit on an empty drive2 and
   pulls and unpacks the platform images that Railpack plans use. It then
   stops BuildKit cleanly and powers off.
   - The images are the builder image and the Railpack frontend, pinned by
     digest.
   - The seed key covers the builder base identity (which includes guest-init
     and BuildKit), the Railpack version, both image digests, the snapshotter
     (FUSE or kernel overlayfs) and the architecture.
   - The run uses the guaranteed builder slot only when the slot is idle, so
     it never delays a build. Builds never outrank tenant wakes, and this run
     never outranks a build.
2. **Each build.** builderd copies the seed with `FICLONE`. On a filesystem
   without reflinks it copies the bytes; a copy that fails turns the seed off
   for that build. builderd passes the copy to vmmd as the build's drive2,
   and sets `buildkit_seed` in the manifest. guest-init mounts drive2 as the
   BuildKit root in place of the directory on drive1. Everything else stays
   on drive1, built as today with `mkfs.ext4 -d`: the source, the manifest,
   the imported dependency cache and the output.
3. **Freshness.** A seed whose key no longer matches is never attached. The
   build runs as today, and the next idle slot builds a new seed. The janitor
   removes seeds of old keys and every drive2 copy, the same way it removes
   build drives.
4. **Deferred cache export** (ADR-20261029171525) keeps drive2 with drive1
   and attaches both to the export VM.

**Compatibility.** `buildkit_seed` is `omitempty`. An older guest ignores it
and keeps BuildKit on drive1, so the build is as today. vmmd's build request
gains an optional drive2 path. An older vmmd ignores it, and the guest, finding
no drive2, falls back the same way. builderd counts a build as seeded only
when the guest reports it in build-done (`buildkit_seeded`).

**Security.**
- The seed holds only public platform images, pulled by digest inside a
  builder microVM from a platform-only manifest. That is the same trust as
  the builder base.
- Each build writes only its own reflink copy, so nothing a build does
  reaches another build or the seed.
- The host never mounts the seed or a copy. It copies the file and attaches
  it as a block device.

## Decision gate

Implement this only if the kernel overlayfs A/B leaves at least 20 s for the
builder image step in a cold Railpack build on a production node. The new
build log timing lines report that step directly. Otherwise this ADR is
withdrawn.

## Rollout

1. Land the guest, vmmd and builderd changes with the switch off. Run
   `test-metal` and `leakcheck`. A metal test builds the seed, then a Node
   app with and without it, and checks that the second build's log shows the
   builder image step cached.
2. Turn the switch on for one node's builderd. Compare the builder image step
   and the build-in-VM time with the other node's for a day, as well as the
   seed's size and its build time.
3. Make it the default if it holds.

## Consequences

- Expected: the builder image step drops from about a minute to a cache hit
  on every Railpack build of every app. The A/B measures what the reflink
  copy and a larger BuildKit root add back.
- About 1.5 GB per seed key on `/srv/fc`; production compute-1 had 24 GB
  free on 2026-10-30. Each build's copy shares the seed's blocks until
  BuildKit writes them.
- A new seed after every builder base or Railpack change. The builds before
  it run cold, as all builds do today.
- vmmd attaches a third drive to builder VMs. VM lifecycle changes need
  `test-metal` and `leakcheck` on a dedicated host.
- Not covered: Dockerfile builds, whose base images vary by app, and the
  `mise install` steps Railpack runs on top of the builder image. Seeding
  common runtime versions is a possible extension once this is measured.


## Gate result (2026-11-12)

Kernel overlayfs is on by default (ADR-20261029105325, accepted 2026-11-02),
and the builder image step still costs a minute of every cold-cache build's
time budget in production: over the seven days to 2026-11-11, 198 of 229
Railpack builds ran `docker-image://ghcr.io/railwayapp/railpack-builder`,
p50 34 s and p90 41 s, about half of the 69 s p50 build command (from the
"slowest build steps" lines builderd writes to the deploy log). That is above
the 20 s gate.

## Implementation notes (2026-11-12)

- **The seed run builds a platform-owned app.** builderd embeds a minimal
  Node app and boots it as a build with `buildkit_seed_only`. Railpack plans
  it exactly as it plans a customer build, so the seed holds the frontend,
  the builder image and the runtime base under the same references and chain
  IDs a customer build resolves. No separate pull logic tracks Railpack's
  choice of builder image, which is pinned by tag inside Railpack's plan.
- **Disk.** The production build disks had 21-22 GB free on 2026-11-12,
  about the 20 GiB build-drive floor. builderd creates a seed only when free
  space covers the floor plus the seed, and drops the seed before refusing a
  build for space; a missing seed only means the build runs as today.
- **Layout.** Seeds live in their own directory beside the build drives,
  which the build-drive janitor does not sweep. vmmd stages drive2 in the
  jail under its own name; the writable layer keeps `layer.ext4`.
- **Warm builders** keep BuildKit on drive1, which their snapshot carries,
  and never take a seed.
- **Slices,** each shipped with the switch off: the guest and manifest
  fields; vmmd's optional drive2; builderd's seed run, copy and janitor; the
  metal test.
