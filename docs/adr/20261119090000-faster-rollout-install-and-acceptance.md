# ADR-20261119090000: Install a release in one decompression; a short canary for the acceptance redeploy

- **Status:** proposed
- **Date:** 2026-11-19
- **Amends:** ADR-113 (canonical tarball install), the production release
  acceptance (`deploy/scripts/production-release-acceptance.sh`)
- **Related:** ADR-20261118060000 (no-drain activations), ADR-1446 (adaptive
  production releases)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

The v0.1.18-rc.357 production rollout took about 48 minutes. With drains gone
(ADR-20261118060000), two fixed costs remained on every release:

1. **The node install took 80–100 s on each node.** `gregalectl release
   install --tarball-path` decompressed the 480 MB release tarball
   (1.4 GB unpacked) three times: once to find `release-manifest.json`,
   which comes after the daemons, once for the hash walk, and once to
   extract. One gzip pass takes 26 s on a 4-vCPU production compute node.
   The daemon restarts that follow are not the cost. Five of the six compute
   daemons restart in about 13 s together; imaged's 122 s on rc.357 was a
   scan of a changed builder base, which happens only when a base changes.
2. **The acceptance redeploy took 259 s of the 448 s acceptance step.** The
   redeploy checks that the previous revision keeps serving while the next
   one rolls out. It used the default adaptive ladder (1%, 10%, 50%, each
   held one to two minutes), so most of the time was spent waiting at
   canary stages.

## Decision

1. **One decompression.** The install stages the tarball once:
   - Each member is written to an owner-only file in a fresh directory under
     the releases root and hashed as it is written. Files get numbered names,
     never archive names. The manifest member is kept in memory (at most
     16 MB).
   - Verification then runs the same checks as before against those hashes:
     the manifest must validate, every catalog daemon, tool and asset must
     match, the archive must contain nothing outside the catalog, and the
     cosign signature over the packed bytes must verify.
   - Only after verification are the allowed members renamed into
     `<release>/bin` under their trusted catalog names, with the same
     symlink and containment checks as `Extract`. A rename leaves a running
     daemon on its old inode, as before.
   - The staging directory is always removed. A corrupted or unsigned archive
     fails before anything reaches `bin`.

   `Tarball.Verify` and `Tarball.Extract` stay for their other callers.
2. **A short canary for the acceptance redeploy.** It runs `--rollout fast`
   (5% then 50%, a minute each). Both revisions still serve through a canary,
   which is what the continuity probes test.

## Consequences

- About 50 s less per node on every release (two of three 26 s passes), and
  one 1.4 GB pass of disk writes instead of a decompression held in memory
  per member.
- About 2–3 minutes less acceptance on every release.
- Release acceptance no longer exercises the adaptive ladder's
  evidence-based advance on production. Its unit and integration tests still
  cover it, and customer releases still use it by default.
