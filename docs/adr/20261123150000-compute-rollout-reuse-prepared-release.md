# ADR 20261123150000 · Compute rollouts reuse what preparation verified

- **Status:** accepted by the owner, 2026-11-23 (picked "A: install-in-prepare"
  and "B: skip re-staging" of the Ansible-trim options)
- **Date:** 2026-11-23
- **Related:** ADR-113 (canonical tarball install), ADR 20261025095348
  (parallel prepare, serialized activation), ADR 20261108170000 (verify the
  bundle before bootstrap extracts from it), ADR 20261117140000 (per-variable
  role hashes)

## Context

A two-node cd-platform release takes ~28 min; compute activation is
serialized node by node, so every second on that path is paid once per node.
Measured on bhs-2/bhs-3 (rc.371–rc.374):

| activation path | share of releases | Ansible time | biggest items |
| --- | --- | --- | --- |
| fast (contract current) | ~2 of 3 | 80–150 s | `gregalectl release install` 32 s, daemon restarts 19 s |
| converge (roles changed) | ~1 of 3 | 200–300 s | ~440 tasks; kernel + VMM + CLI staging 26 s |

Both items repeat work preparation already did on the same bytes:

- `release install --tarball-path` reads the 489 MB bundle, decompresses it
  three times (manifest member, per-member hash walk, extraction), writes a
  temporary copy for cosign and writes 1.9 GB with an fsync per file — while
  the node is drained or about to restart. Preparation had extracted the same
  verified bundle ten minutes earlier.
- The first play stages the guest kernel, the release-built Firecracker VMM
  and (third play) the operator CLI by decompressing the bundle once each,
  the VMM twice (`tar --list`, then extract). A converge-path activation runs
  the playbook from the top and repeats all of it.

OVH S3 is not a factor: shared-base reads run at 40–70 MB/s.

## Decision

**A. Preparation stages the release; activation reuses it.**

- `gregalectl release install --stage-only --tarball-path P` runs the full
  canonical install up to, not including, the flip, into a **staging area
  beside the releases root** (`/opt/faas/staged-releases/<sha>`, same
  filesystem): cosign, hash walk, extraction, retained triple, on-disk
  `releaseinstall.Verify`. It then writes `staged-install.json` last: the
  sha256 of the retained tarball, cosign bundle and SBoM, the signed-manifest
  digest and the cosign identity. It never touches the releases root, the
  current release or the database. It skips the active release and any
  release already in the releases root, and it clears every other entry of
  the staging area first (aborted rollouts' stages, interrupted stages), so
  the area holds at most one stage.
- An ordinary `release install --tarball-path P` moves a staged tree into
  `<releases-root>/<sha>` with one rename only when all of these hold, and
  otherwise logs why and installs from the tarball as before:
  - the release is not already in the releases root;
  - the marker names this git SHA and format;
  - P and its sibling bundle/SBoM hash to the marker's digests, and so do the
    retained copies;
  - the staged manifest's signed digest and identity match the marker;
  - every staged binary hashes to the manifest (a damaged stage falls back to
    the full install, which repairs it, instead of failing every retry);
  - cosign verifies the retained tarball again and reports that identity.

  The marker is dropped after the move, the staged tree is discarded either
  way, and the caller's on-disk `Verify`, SBoM gate and flip run unchanged.
- node_join's prepare phase starts the stage in the background (`async`)
  beside the runtime-base pre-stage and waits for it before recording the
  prepared marker. It is best effort: a failure (or an older CLI after a
  rollback) is reported and activation installs from the tarball.

**B. One decompression for kernel, VMM and CLI, skipped on a rerun.**

- One task, after the bundle's cosign check, extracts `vmlinux`,
  `gregalectl` and `firecracker-vmm` in a single tar pass into
  `/var/lib/faas/bootstrap/` (`gregalectl-release` for the CLI) and writes
  `release-members.stamp`: the bundle's sha256 and each file's. With a stamp
  for the same bundle whose files still hash the same, it changes nothing.
  The kernel is now staged after the signature check instead of before it.
- The third play promotes the CLI by copying `gregalectl-release` when the
  stamp matches the bundle and the file, and extracts from the bundle as
  before otherwise.

## Consequences

- Expected: activation's install drops from ~32 s to a few seconds of hashing
  plus cosign, on every release and every node; a converge-path activation
  also skips ~26 s of staging; preparation saves ~15 s of staging and runs the
  release extraction in parallel with the base pre-stage.
- Trust is unchanged: cosign still runs at activation on the exact bytes, and
  every binary is still hashed against the manifest before the flip. The
  marker and stamp are root-owned files under root-owned directories; an
  attacker who can rewrite them can already replace the binaries.
- Stages never sit in the releases root, so release retention, deploycontroller
  rollback selection and deployment-bundle verification never see a release
  that was prepared but not activated, and need no change. (A first version
  staged inside the releases root behind a marker; review showed it leaking
  into all three.)
- The stage runs while the node serves tenants, so it runs under
  `systemd-run` with `MemoryMax=2G` and low CPU/IO weight (bhs-2 had 122 of
  125 GB available); hitting a limit only costs the reuse. Disk: at most one
  staged release (≈2 GB) beside the releases root, and it becomes the
  installed release by rename.
- `release install --tarball-path` takes an exclusive lock on the staging
  area (`staged-releases/.install.lock`), so a stage left running by a failed
  prepare play and a rerun or activation never work on it at once. The wait
  is bounded (15 min) and the stage unit has `RuntimeMaxSec=840`, so a hung
  stage cannot hold a drained node's activation indefinitely.
- The first release carrying this changes `node_join.yml`, so its activation
  takes the converge path once (stale contract). The install saving applies
  from that release on, because preparation runs the new CLI from the release
  being installed.
- Not done: parallel non-canary node rollout, building runtime bases in the
  release workflow, a cheaper ingress probe, parallel daemon restarts. They
  remain the larger levers (see the 2026-11-23 rollout breakdown).
