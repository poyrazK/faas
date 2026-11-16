# ADR 20261116190000 · A node keeps each runtime base generation in its own cache file, so a release that changes a base restarts vmmd without a drain

- **Status:** proposed (2026-11-16)
- **Date:** 2026-11-16
- **Amends:**
  - [ADR-20261024085817](20261024085817-rollout-base-generation-and-drain-completion.md):
    a changed generation (`base-generation-check` exit 3) no longer defers
    the pre-stage past the drain barrier, and no longer forces the drain.
    Exit 4 and the rescue keep their behaviour.
  - [ADR-20261026181418](20261026181418-generation-addressed-runtime-base-keys.md):
    the cache entry of a routed base is now named by its generation key too,
    not only its shared copy.
- **Implements:** trigger B of
  [ADR-20261116170000](20261116170000-releases-without-node-drains.md)
  (releases without node drains), with the owner's choices of 2026-11-16.
- **Related:** ADR-005 (cold boot always works), ADR-510 (snapshot backing
  identity), ADR-20261024191249 (adoption gate)

## Context

The owner's goal is that no release drains a compute node. Trigger A
(guests the candidate vmmd refuses) is handled by ADR-20261116170000. This
ADR handles trigger B: every release that changes a runtime base generation
drains every node.

The drain exists because a node's cache kept **one file and one generation
marker per base**, under its logical name (`base/base-amd64.ext4`). The
serving vmmd boots that file only while its marker names the vmmd's own
generation (`ensureBaseGeneration`). A pre-stage of the next generation
replaced the file and the marker, and the serving vmmd then refused every
new guest on that base (rc.290, 2026-10-22). ADR-20261024085817 therefore
moved the pre-stage behind the drain barrier and made every generation
change drain.

Shared storage already holds each generation under its own keys
(ADR-20261026181418). Only the node-local cache still holds one generation.

Facts the design relies on:

- A running guest keeps its base: vmmd hard-links drive0 into the jail
  (`stageReadOnly`), so replacing or evicting the cache file never touches
  it.
- `adoption-check` does not depend on base generations.
- A snapshot captured on another base is already refused and cold-boots once
  (ADR-510). That stays.

## Decision

Owner choices (2026-11-16): a per-generation cache file; unpin the old copy
when the new vmmd starts; keep refusing snapshots of the old base; on by
default.

1. **Per-generation cache file.** `LocalCacheBackend.cacheFileFor` names a
   routed key's file by its parent (generation) key, and its `.meta` sidecar
   records that key. Callers keep the logical name everywhere: drive0,
   snapshots, producer records. A release before this one does not route its
   cache, so it keeps reading and writing the file under the logical name.
   The two never share a file. A candidate's pre-stage writes only its own
   generation's file and marker.
2. **Carry-over, not download.** At startup, imaged and vmmd route each gated
   base, then `AdoptCachedGeneration` hard-links the logical file into the
   generation's file when the logical marker already names that generation.
   This covers the base, digest and scan, with the base last. The first
   release with this change downloads nothing the node already holds.
3. **Unpin at restart.** vmmd's startup (`imaged.UnpinOtherBaseGenerations`)
   removes the generation markers of the logical files and of every other
   generation's files of the bases it routes. The entries stay cached and
   become evictable. imaged never unpins, because its pre-stage runs beside
   the serving vmmd. Removing the logical marker also means
   `base-generation-check` cannot mistake a stale logical file for the
   serving vmmd's base.
4. **The checks read the right file.**
   - `base-generation-check` reads the serving generation's own file first,
     then the logical one, which a vmmd from before this change still boots.
     Exit 4 (the serving generation is gone from the cache) can no longer be
     caused by a candidate's pre-stage.
   - `JudgeBaseAdoption`, when its storage is not routed, reads the
     generation's file first.
   - `base-adoption-check --candidate-env-file` judges the candidate's
     generations before a restart.
5. **`node_join.yml`.**
   - Exit 3 pre-stages before the drain decision, including in phased
     `prepare`.
   - Before restarting without a drain, the candidate binary's
     `base-adoption-check` must pass: each changed base is cached here, or
     published under its generation's keys. It retries 6 × 10 s.
   - A base the candidate cannot adopt drains the node as before, and so do
     exit 4 and `faas_join_side_by_side_bases=false`. The post-install
     adoption gate still runs after every drain.

## Consequences

- A release that changes a runtime base restarts vmmd in place when the
  candidate adopts every guest (ADR-20261116170000) and every changed base.
  Running guests keep their old base. New guests and restores use the new
  one once the new vmmd runs.
- Disk: a changed base is held twice until eviction reclaims the unpinned
  copy (roughly 150–400 MB per changed base). A carried-over base shares its
  bytes through the hard link, but the cache budget counts it twice until the
  logical copy is evicted.
- Snapshots of the old base still cold-boot once and are re-captured
  (unchanged, ADR-510).
- A vmmd restart that is not part of a rollout (a crash) while a candidate's
  pre-stage is pending unpins the candidate's files. The candidate vmmd then
  re-reads them from shared storage. That costs a download, not correctness.
- Rollback to a release before this change: its vmmd boots the logical file.
  If the new vmmd unpinned it and eviction removed it, the old vmmd fetches
  its generation from shared storage at first boot, under the
  ADR-20261026181418 legacy rules.
- Native acceptance (`make test-metal`, `make leakcheck`) on a dedicated KVM
  host is owed before release. The first production proof is a release that
  changes a base, rolled out with "Runtime base generations change:
  pre-staging the new generation beside the serving one" and "Restarting
  without a drain" in each node's log.
