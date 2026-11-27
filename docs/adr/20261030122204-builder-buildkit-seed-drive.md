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
- **builderd** (`FAAS_BUILD_BUILDKIT_SEED`, off by default) checks every
  `api.BuildKitSeedCheckEvery` (1 min). It boots the seed run only when no
  build holds the builder slot and free space covers the build-drive floor
  plus `api.BuildKitSeedReserveBytes` (4 GiB). After a failed run it waits
  `api.BuildKitSeedRetryMin` (2 min), doubling per failure up to
  `api.BuildKitSeedRetryMax` (30 min): the metal run's first seed attempt
  met the new builder base's scan still pending. Drain stops a seed run's VM as it
  does a build's.
- **Room for the seed (2026-11-16).** The artifact cache keeps `/srv/fc`
  nearly full (19-21 GB free on both production nodes), so a seed run first
  asks the cache for the build-drive floor plus the seed reserve, as a build
  asks it for the floor. Without that a seed was never made. The seed thus
  outranks the cache's oldest artifacts, which can be fetched again, and the
  cache is smaller by the seed's real size (about 1.5-2 GB).
- **Reflink only.** A copy that cannot share the seed's blocks is not made:
  the seed is a 28 GiB sparse image, so a byte copy would cost more than the
  pull it saves. Without a copy the build runs as today. `/srv/fc` is XFS
  with `reflink=1` on the production nodes.
- **Seed key.** It covers the builder base identity and digest (guest-init,
  BuildKit and Railpack, whose plan pins the builder image), the platform,
  the snapshotter, the seed app's digest and a format version.
- **Eligible builds.** Railpack builds only: not Dockerfile builds (by
  framework or Compose options), warm builders or the seed run. A deferred
  cache export attaches the build's copy to the export VM only when the
  guest reported `buildkit_seeded`; otherwise the copy goes when the build
  ends.

## Production canary finding (2026-11-17)

The first production canary (compute-2, `FAAS_BUILD_BUILDKIT_SEED=1`)
made a seed, and every build after it ran unseeded: builderd could not open
the seed (`permission denied`). vmmd stages a builder's writable drives for
its jail uid by chmodding the source 0600 and chowning it, on the
assumption that builderd only removes the file afterwards. The seed run's
drive is the one builderd keeps and reflinks for every build. The metal
test ran as root, which reads any file, so it passed. The canary was rolled
back the same day.

- vmmd records a builder's BuildKit seed drive when it stages it and, once
  the VM has exited and its binds are unmounted, gives the file back to the
  owner and group of the directory builderd keeps it in (the build export
  handoff does the same for exports). A failure is logged; it costs only
  the seed.
- builderd installs a seed only if it can open it, and drops a ready seed a
  build cannot open, so the next idle slot makes one. With an older vmmd
  the seed run therefore fails and backs off instead of installing a seed
  no build can use.
- The metal test checks that the installed seed has its directory's owner,
  which fails without the handoff even as root.

## Second production canary (2026-11-18)

The second canary ran on fsn-2 (compute-1) on rc.355, which carries the
handoff, from 2026-11-18 19:59Z to 2026-11-19 07:15Z. fsn-3 ran without the
seed in the same window.

| | Builds | vm_build p50 | vm_build p90 | build command p50 |
|---|---|---|---|---|
| fsn-2, seeded | 8 | 41 s | 72 s | 31 s |
| fsn-3, unseeded, same window | 7 | 74 s | 108 s | 67 s |
| fsn-2, unseeded, three days before | 165 | 85 s | 120 s | 75 s |

- Every build on fsn-2 ran seeded, and builderd logged no error.
- The seed belongs to builderd's directory owner. It takes about 2.4 GiB of
  `/srv/fc`.
- A release in the window changed the platform images. builderd made a new
  seed in 87 s and dropped the old one.

production-us turns the seed on for every compute node
(`builderd_buildkit_seed` in `deploy/ansible/group_vars/compute_nodes/perf_switches.yml`).
The daemon default stays off.
