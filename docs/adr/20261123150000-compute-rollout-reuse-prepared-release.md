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
  canonical install up to, not including, the flip: cosign, hash walk,
  extraction, retained triple, then the on-disk `releaseinstall.Verify`. It
  then writes `<release>/staged-install.json` last: the sha256 of the
  tarball, cosign bundle and SBoM, the signed-manifest digest and the cosign
  identity. It never flips, runs the SBoM gate, prunes or touches the
  database.
- An ordinary `release install --tarball-path P` reuses a staged tree only
  when all of these hold, and otherwise logs why and installs from the
  tarball as before:
  - the marker names this git SHA and format;
  - P and its sibling bundle/SBoM hash to the marker's digests, and so do the
    retained copies in the release directory;
  - the on-disk manifest's signed digest and identity match the marker;
  - cosign verifies the retained tarball again and reports that identity.

  The caller's on-disk `Verify` (every binary against the manifest), SBoM
  gate and flip then run unchanged. A full install removes the marker before
  extracting, so an interrupted extraction is never reused.
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
- A release directory now exists before activation. If activation never
  happens, the directory counts as one of the kept previous releases until
  the next install's prune.
- The first release carrying this changes `node_join.yml`, so its activation
  takes the converge path once (stale contract). The install saving applies
  from that release on, because preparation runs the new CLI from the release
  being installed.
- Not done: parallel non-canary node rollout, building runtime bases in the
  release workflow, a cheaper ingress probe, parallel daemon restarts. They
  remain the larger levers (see the 2026-11-23 rollout breakdown).
