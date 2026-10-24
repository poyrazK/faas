# ADR 20261024191249 · A rolled node publishes every runtime base its release configures, and is not activated until its vmmd can adopt them

- **Status:** proposed (2026-10-24)
- **Date:** 2026-10-24
- **Amends:** [ADR-20261024085817](20261024085817-rollout-base-generation-and-drain-completion.md)
  (the pre-stage behind the drain barrier, and the rescue that never
  re-activates a node whose cache moved)
- **Related:** ADR-005 (cold boot always works), ADR-510 (snapshot backing
  identity), ADR-567 / ADR-632 (runtime base convergence), H10-95 (deferred
  child bases), H10-103 (scan refusal on unchanged bytes),
  [release compatibility](../release-compatibility.md)
- **Incident:** production-us, 2026-10-24, rc.293 compute rollout
  (cd-platform run 40112989490). The rollout was cancelled before fsn-3, and
  fsn-2 was rolled back to rc.290.

## Context

### What happened

rc.293 moved the runtime bases to Debian 13. `base-minimal` changed
generation (`sha256:f615dc4f…`), and `runner-node24` and `runner-python312`
were rebuilt. All 96 apps were owned by fsn-3, and fsn-2 rolled first.

| UTC | Event |
|---|---|
| 18:25:27 | `base-generation-check` exits 3 (a generation changes), so the pre-stage is deferred behind the drain barrier (ADR-20261024085817). |
| 18:25:33 | fsn-2 drains. It holds no instances, so the drain is immediate. |
| 18:25:34–18:26:26 | The pre-stage runs. It logs `imaged runtime-base pre-stage complete` with `assigned_minimal: false, assigned_runtimes: []`, so it staged and published nothing. |
| 18:29–18:33 | rc.293 is installed and the daemons restart. |
| 18:33:54 | The readiness gate passes. The controller activates fsn-2. |
| after | Every wake placed on fsn-2 fails 503 `scan_critical` "runtime base … could not adopt its configured generation" until fsn-2 is drained by hand. |
| 18:34–18:38 | fsn-3's job starts and is cancelled: rolling it would drain the only working node. |

### Why fsn-2 could not boot

- **The pre-stage stages only the bases of apps this node owns.**
  `assignedBasePlan` reads `ListAppsByNodeID` and `ListDeploymentsByNodeID`.
  fsn-2 owned nothing, so its pre-stage and its serving imaged staged no
  runtime base.
- **Nothing else publishes a changed generation.** Convergence (ADR-567 and
  ADR-632) adopts only a publication built from the same source ref.
  Deploy-time staging runs only when an app on that runtime deploys. The
  shared scan sidecar for `base/base-amd64.ext4` therefore kept naming
  rc.290's generation (`5ca69ea…`).
- **vmmd refuses a base it cannot adopt.** fsn-2's rc.293 vmmd found that
  its cache marker was not its configured generation. It refreshed the scan
  sidecar from shared storage, found `5ca69ea…`, and refused every boot on the
  base: cold boot, restore and migration adoption (`ensureBaseGeneration`,
  `bringUpScanCheck`).
- **No step checked adoption before activation.** The readiness gate checks
  daemons and dependencies, not whether the vmmd can boot the release's
  bases. ADR-20261024085817's check guards only the *serving* release in the
  drain rescue.

### A second ordering defect

vmmd's `ensureBaseGeneration` reads the shared scan sidecar as the commit
marker. Only once it names the configured generation does vmmd pull the
base and its digest sidecar. That depends on imaged writing the base, then
the digest, then the scan.

`invalidateScanBeforeRebuild` breaks that order. Before rebuilding a verified
base, it writes a fail-closed scan sidecar, and that sidecar named the **new**
ref while the shared base still held the previous bytes. A node on the new
release that missed its cache in that window would do the following:

1. It accepts the refusal as the commit marker.
2. It pulls the previous base and digest.
3. It marks them as the new generation.
4. Once the real scan lands, it boots the previous bytes as the new generation
   for good, because its marker now matches.

The window was small. This decision makes it the normal path, because the
first node to roll now rebuilds every changed base.

## Options weighed

**(a) The pre-stage stages every configured base unconditionally.** This is
simple, but it is expensive. Any release that changes guest-init (most
releases do) defeats producer reuse, and every rolled node would rebuild and
scan all seven bases and the Debian parent. Every node would also download
every base into its cache, including bases it never serves. Rejected.

**(b) The pre-stage publishes each configured base that this node could not
adopt.** Chosen. A base the node's cache already commits, or that shared
storage already publishes at the candidate generation, is left alone. Only
the first node to roll a changed generation builds and scans it.

**(c) Plan from every app in the fleet (`ListAllApps`).** This is precise
about use, but the activation gate runs in vmmd, which has no database
read, and could not mirror it. A published base that no app uses would then
pass the pre-stage and fail the gate. Rejected in favour of a rule both sides
can evaluate from storage alone.

**(d) The serving imaged reconciles every configured base.** Rejected. Every
serving restart, including no-drain restarts and crash restarts, would build
bases for the whole fleet while the node takes traffic. ADR-20261024085817
deliberately keeps changed-generation builds behind the drain barrier.

**(e) Gate activation only.** This is necessary but not sufficient. It would
have left fsn-2 drained instead of serving 503s, but no rollout could have
completed.

## Decision

### 1. One adoption verdict, shared by imaged and vmmd

`imaged.JudgeBaseAdoption` mirrors `ensureBaseGeneration` for one base on
one node. It returns one of four verdicts:

| Verdict | Meaning | Adoptable |
|---|---|---|
| `cached` | This node's cache marker is the configured generation. vmmd never reads the shared copy. | yes |
| `published` | The shared scan sidecar names the configured generation, using vmmd's own matching (`fcvm.ScanNamesConfiguredBase`). vmmd adopts it on its first boot here. | yes |
| `unpublished` | Neither the scan nor the digest sidecar exists. No node ever staged the base, so no deployment has used it, and the first deployment publishes it (unchanged behaviour). | yes |
| `stale` | The shared copy is another generation, its scan is missing or torn, or the scan is a rebuild's refusal. The cache does not hold the configured generation. vmmd refuses every boot here. | no |

The shared copy is read with the new `storage.GetCanonical`. It reads the
read-through cache's parent and never installs or replaces a cache entry, so
a serving vmmd on the node keeps reading the scan it last adopted. Reading
through the cache could otherwise cache another node's in-flight refusal and
fail this node's boots.

`imaged.GatedRuntimeBases()` lists the bases that vmmd gates by generation:
minimal plus every function runtime, excluding the Debian parent.
`configuredRuntimeBaseGenerations` in vmmd now iterates this list, so the
pre-stage publishes exactly the set the boot gate checks.

### 2. The pre-stage publishes every stale configured base

The pre-stage is `FAAS_IMAGED_PRESTAGE_ONLY=1`. After
`EnsureAssignedBases`, it runs `EnsureReleaseBases`:

- Every gated runtime that the assigned pass did not stage is judged.
- Each `stale` base is staged through `EnsureMinimalBase` or
  `EnsureRuntimeBase`. The latter stages the Debian parent first when a child
  needs it.
- H10-95's deferral rule applies unchanged.
- `cached`, `published` and `unpublished` bases are not touched.

The pre-stage log lists the staged, current, unpublished and deferred bases,
and the duration of each publication.

**Serving mode is unchanged.** It stays ownership-based, for the reason in
(d). Every node that rolls runs the pre-stage first, so the serving imaged
never needs to publish for the fleet. The one exception is a child base that
H10-95 defers, which falls to the serving imaged, and that imaged stages only
its own apps' bases. The gate below catches such a base as `stale`. H10-95
deferral needs a serving vmmd older than verified parents, which no
production node runs today.

### 3. The scan sidecar is the last write of a publication

A rebuild's fail-closed scan sidecar now names no generation
(`uncommittedBaseScanImage`). It still refuses every boot on the old result,
as H10-103 requires.

Only `finishVerifiedBase` names the generation. It runs after the base and
its digest sidecar are published, and the cache marker is committed after
that. The legacy staging paths already wrote base, then digest, then scan.

A test records every storage `Put` of a first publication and of a
guest-init rebuild. It fails if any scan sidecar names the new ref before the
base and digest are written.

### 4. `vmmd base-adoption-check` gates activation

`vmmd base-adoption-check` runs from the **installed** binary after the
restart. It reads the serving vmmd's configured generations from
`/proc/<pid>/environ` and opens storage the way that vmmd does. It judges
every gated base and prints one JSON report. Exit codes:

- `0`: every base is adoptable.
- `3`: a base is `stale`.
- `1`: the check failed.

`node_join.yml` runs it after `Require dependency-aware readiness after
release installation`, and before the play reports readiness. The controller
activates the node only after the play succeeds.

- **Bounded wait.** 18 tries at 10 s (3 minutes) cover a publication still in
  flight from the serving imaged or another node.
- **On timeout.** The rescue fails the play and **never activates**. The
  node stays drained. The message names each stale base with its configured,
  published and cached generations, or the check's error, says that every
  wake there would fail 503 `scan_critical`, and says to re-run the rollout,
  whose pre-stage publishes the missing bases.
- **No-drain restarts skip it.** They require `base-generation-check` exit 0,
  so they change no generation and the node never left service.

## Consequences

- A release that changes a runtime base generation can no longer return a
  node to service on a base its vmmd cannot boot. Either the pre-stage
  publishes the base, or the gate keeps the node drained and names the base.
  With this change, rc.293's fsn-2 would have built and published three bases
  (or four, if the Debian parent changed) behind its drain barrier, and
  activated cleanly.
- **Runtime cost.** The rc.293 fsn-2 join took 21 minutes from `deploy
  join-node` to the controller, with a 52-second pre-stage that built nothing.
  The rc.290 fsn-2 pre-stage built and scanned two changed bases in 5 minutes,
  about 2.5 minutes per base. Each Grype scan is bounded by
  `ApplicationStandardArtifactScanTimeout` (5 minutes).
  - **First node.** The first node to roll a changed generation now pays for
    every changed base that is in use, about 8–10 minutes for an rc.293-shaped
    release. That work used to be split across the owning nodes, or skipped,
    which caused this incident.
  - **Later nodes.** They find the bases `published` and build nothing. Their
    assigned bases reuse the first node's producer and scan record and only
    download.
  - **Worst case.** A release that changes all seven gated bases and the
    parent adds about 20 minutes to the first node. Together with a full drain,
    that exceeds the 40-minute `join-node` timeout in `cd-compute.yml`. Such a
    release must raise that timeout, or it fails safe: the node stays drained,
    and a resumed run reuses whatever finished (follow-up 2).
- **Mixed fleet (release N beside N-1).** Before this change, a changed
  generation reached the shared key only when a rolled node owned an app on
  it. Now the first rolled node publishes every changed base that is in use.
  - **N-1 nodes keep working** on every base their cache commits. That
    includes every base of the apps they own, which their imaged stages at
    startup.
  - **The cache-miss gap from ADR-20261024085817 opens earlier.** An N-1 node
    that boots a changed base it never cached, such as a wake placed there for
    an app it does not own, refuses it until it rolls. This gap opens at the
    first node's pre-stage for every changed base, rather than only for owned
    ones. Before this change the same boots failed on the rolled node instead
    (this incident). Neither ordering closes the gap. Generation-addressed
    keys (ADR-20261024085817 option b) remain the fix and are not attempted
    here.
  - **A deploy on an N-1 node can still republish N-1.** Deploy-time staging
    of a changed runtime on an N-1 node rebuilds N-1 over N, which was already
    possible for owned bases. Rolled nodes keep booting it from their cache
    (`cached`). A node that has not activated yet sees it as `stale`:
    - its own pre-stage publishes N again;
    - if the republish lands between that pre-stage and the gate, the gate
      keeps the node drained, and re-running the rollout fixes it.
  - **The publication-order fix narrows the mixed window.** A node on N can no
    longer commit N-1 bytes as N during a rebuild.
- **Rollbacks.** A compute rollback that runs a release containing this
  change republishes the rollback's generation of every stale base. That
  favours the node being rolled, at the cost of uncached bases on nodes still
  on the newer release. Rollbacks to rc.290 run rc.290's pre-stage and keep
  the old ownership-only behaviour.
- **New surface.**
  - `storage.GetCanonical` and `LocalCacheBackend.GetCanonical`.
  - `fcvm.ScanNamesConfiguredBase`, exported unchanged.
  - The `vmmd base-adoption-check` subcommand.
  - New pre-stage log fields: `release_bases_published`,
    `release_bases_current` and `release_bases_unpublished`.
  - No migration, proto, metric or limit change.
- **Native acceptance owed before the rc.293 retry.** Run a two-node rollout
  of a generation-changing release on the internal test fleet, where the first
  node owns no app, and confirm that its pre-stage publishes and its gate
  passes. The macOS development host cannot run it.

## Follow-ups

1. Generation-addressed runtime base keys (ADR-20261024085817 option b),
   which close the N-1 cache-miss gap and the rollback asymmetry.
2. Make the `join-node` timeout scale with the bases a release changes, or
   publish changed bases in a dedicated serialized step before the first
   activation, so a full-matrix base change fits the rollout budget.
3. Gate the deploy-time staging of a runtime base on an N-1 node, so a
   deploy cannot republish an older generation during a rollout.
