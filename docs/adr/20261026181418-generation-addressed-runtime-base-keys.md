# ADR 20261026181418 · Each runtime base generation has its own shared keys, so releases N and N-1 never overwrite each other's

- **Status:** proposed (2026-10-26)
- **Date:** 2026-10-26
- **Amends:**
  - [ADR-20261024085817](20261024085817-rollout-base-generation-and-drain-completion.md):
    implements its option (b) as cache routing, and closes its first two
    residual risks (an uncached base on an N-1 node, and a compute rollback
    across a generation change).
  - [ADR-20261024191249](20261024191249-rollout-publishes-release-bases-and-gates-activation.md):
    adds the `legacy` adoption verdict, which the pre-stage republishes.
    Closes its follow-up 1. Its follow-up 3 (gate deploy-time staging on
    N-1 nodes) is superseded.
- **Related:** ADR-005 (cold boot always works), ADR-435 (verified base
  producers), ADR-510 (snapshot backing identity), ADR-567 / ADR-632
  (runtime base convergence), ADR-595 (application standards), ADR-736
  (immutable runtime releases), [release compatibility](../release-compatibility.md)
- **Incident:** production-us, 2026-10-26, rc.296 compute rollout. The
  rollout was aborted.

## Context

### What happened

The control plane was on rc.296. fsn-2 had rolled to rc.296 and was drained,
and fsn-3 was still on rc.294.

| UTC | Event |
|---|---|
| 16:53 | fsn-2's rc.296 pre-stage (ADR-20261024191249) publishes rc.296's `base-minimal` generation to `base/base-amd64.ext4`, its digest sidecar and its scan sidecar. |
| 17:03 | A source build runs on fsn-3. rc.294's serving imaged restages `base-minimal` and rewrites the shared scan sidecar with rc.294's generation (`ghcr.io/poyrazk/base-minimal@sha256:19dd3482…`). |
| after | fsn-2's rc.296 vmmd refuses every cold boot with `scan_critical`: `runtime base "base/base-amd64.ext4" could not adopt its configured generation: refreshed scan generation … does not match configured runtime base`. |
| later | fsn-3 starts draining. Its wakes move to fsn-2 and fail. The rollout is aborted. |

### Why fsn-2 failed

Each runtime base had one logical key in shared storage, holding one
generation for the whole fleet. Whichever release staged last decided what
every other node fetched on a cache miss. The bases are `base-minimal`
(`base/base-<arch>.ext4`) and the function runtimes
(`base/runner-<runtime>-<arch>.ext4`).

ADR-20261024085817 and ADR-20261024191249 already recorded one direction of
this: once a node on release N publishes N's generation, an N-1 node refuses
any changed base it has not cached. This incident is the other direction. An
N-1 imaged republished N-1's generation, and N's nodes refused the base.

ADR-20261024191249's follow-up 3, gating deploy-time staging on N-1 nodes,
cannot help here: N-1's code shipped before anyone knew it needed the gate.
Every fix therefore has to live in the code that reads and writes the bases,
so that it protects the releases that come after it.

## Options weighed

**(a) Gate deploy-time staging on N-1 nodes.** Rejected. A release's code is
fixed when it ships, so this protects only later pairs. It also leaves
rollbacks and the N-1 cache-miss gap open.

**(b) Generation-addressed keys everywhere.** Nodes would boot, snapshot and
record producers under per-generation keys, through schedd, ADR-510 and the
producer catalogue. Rejected for now. A base producer's artifact key must be
the logical key in several places:
- the ADR-595 application-standard SQL functions;
- the ADR-435 lineage checks;
- `imagechain.ValidBaseKey`;
- vmmd's parent-materialization allow-list.

Changing those needs schema and RPC changes staged over several releases.

**(c) Compare-and-set on the logical key** (write only when it is absent or
already names this generation). Rejected. Releases already in production
would not follow it, and one copy per base still leaves an N-1 node that
misses its cache with nothing to read.

**(d) Route each generation to its own shared keys inside the node cache.**
Chosen. A node keeps the logical name everywhere it already uses one: the
cache entry, the generation marker, drive0, snapshots and producer records.
Only shared storage changes. Each generation lives under its own keys, and a
release reads and writes only the keys of the generation it configures.

## Decision

### 1. Key layout

`storagekey.BaseGeneration(baseKey, generation)` names one generation of a
base:

| Logical key (node cache) | Shared key of generation G |
|---|---|
| `base/base-amd64.ext4` | `base/generations/<sha256(G)>/base-amd64.ext4` |
| `base/base-amd64.ext4.digest` | `base/generations/<sha256(G)>/base-amd64.ext4.digest` |
| `base/base-amd64.ext4.content` | `base/generations/<sha256(G)>/base-amd64.ext4.content` |
| `scans/base-amd64.ext4.scan.json` | `scans/generations/<sha256(G)>/base-amd64.ext4.scan.json` |

G is the exact image ref the release configures, the same string vmmd
compares (`FAAS_DEPLOY_BASE_REF_*`). A mirror ref of the same digest is
therefore its own generation, just as vmmd already treats it.

Only the bases vmmd admits by generation are routed: `GatedRuntimeBases`,
which is `base-minimal` plus every function runtime. The builder base, the
Debian parent, execution-profile bases and ADR-736 runtime releases keep
their keys. The runtime releases are already content-addressed.

### 2. The cache routes shared reads and writes

`LocalCacheBackend.RouteGeneration(key, parentKey)` maps a key to its name in
the parent store:
- `Put` writes the parent's `parentKey` and caches the bytes under `key`.
- A `Get` miss, `Refresh`, `GetCanonical`, `Exists` and `Delete` use
  `parentKey`.
- The cache entry and its generation marker stay under `key`.

Routing is refused when the parent keeps either key on a node-local backend,
such as single-box storage or a local `base/` prefix. That copy is the file
the node boots, and no other node shares it.

At startup imaged (serving and pre-stage) and vmmd route every gated base's
group to the generation their environment configures:
- imaged: `Handler.RouteBaseGenerations`;
- vmmd: `imaged.RouteBaseGenerations` with `imaged.ConfiguredBaseGenerations`.

imaged resolves each ref exactly as `EnsureBases` does. A base whose ref does
not resolve stays unrouted, and staging it fails on the same error.
`EnsureBaseExt4` refuses to stage a ref under a key routed to another
generation.

So a release with this change never writes the shared logical key of a gated
base. It never moves the generation that a node on a release before it reads.

### 3. vmmd adopts its own generation

`ensureBaseGeneration` keeps its fast path: the cache marker equals the
configured generation. Otherwise it adopts the group from these shared keys
(`LocalCacheBackend.RefreshFrom`), installing each under the logical name:

1. **The generation's own keys.** The scan sidecar comes first, as the commit
   marker, then the base and the digest sidecar.
2. **The legacy logical keys,** only when the generation's scan does not exist
   (`ErrNotFound`). This happens when a release before this one published the
   generation. They are adopted only while their scan names the generation.
   This is the expand step: release N still reads N-1's legacy keys.
3. **Refusal,** when the generation's own scan names nothing (a rebuild of it
   in flight). vmmd refuses instead of adopting older bytes from the legacy
   key, as before.

vmmd then marks the digest, the scan and the base, with the base last as the
commit record. The marks keep the whole group out of cache eviction, so a
later scan read never falls through to another release's copy. H10-103's
re-check (`refreshCachedScan`) also reads the generation's scan, and the
legacy scan only when it names the generation.

### 4. A reused base is published under its generation first

A verified reuse (ADR-435) proves the base bytes from this node's cache, which
says nothing about the generation's shared key. The first time a release with
this change reuses a base that an older release staged, `finishVerifiedBase`
does this:
1. uploads the verified cached bytes to the generation key, when that key is
   missing (`LocalCacheBackend.PublishCached`);
2. records its content sidecar;
3. writes the digest and scan sidecars.

A scan therefore never names a generation whose base is missing. This
migration is an upload, not a rebuild.

### 5. Adoption verdicts

`JudgeBaseAdoption` reads the keys in the same order vmmd does.

| Verdict | Meaning | Adoptable | Pre-stage |
|---|---|---|---|
| `cached` | The cache marker is the configured generation. | yes | leaves it |
| `published` | The generation's own scan names it. | yes | leaves it |
| `legacy` | Only the logical key names it. A node on a release before this one can restage that key at any time. | yes | **publishes it under its own keys** |
| `unpublished` | No generation of the base exists anywhere: none under its own keys, none under the logical key, and none listed under `base/generations/`. | yes | leaves it |
| `stale` | Otherwise. | no | publishes it |

Releases with this change never write the logical key, so a base first
published after it shows up only under some generation's keys. The
`unpublished` check therefore lists `base/generations/`. GCS, the OCI
fallback and the prefix router all list. On storage no other node shares, the
logical key is the publication, and the verdicts are those of
ADR-20261024191249.

## Consequences

**Mixed fleet, whichever node stages last.**
- When N-1 also has this change, N boots N and N-1 boots N-1. Neither
  release can reach the other's keys.
- When N-1 predates this change, N never writes the logical key, so N-1's
  cache misses keep reading N-1's generation. N stops reading the logical key
  once its own generation is published. The pre-stage publishes it before the
  node is activated.
- `TestMixedReleaseFleetBootsEachGeneration` and
  `TestStagingKeepsEachReleasesGeneration` replay the 2026-10-26 order (an
  N-1 imaged restaging after N's pre-stage), and both nodes boot their own
  generation.

**Compute rollback.**
- A node rolled back to a release with this change re-adopts the older
  generation from that generation's keys, and rolling forward re-adopts the
  newer one.
- A rollback to a release before this change reads the logical key. No
  release with this change ever wrote it, so it holds whatever the last such
  release published.

**The ADR-20261024085817 exposure becomes recoverable.** A serving vmmd with
this change whose cache a candidate pre-stage moved re-adopts its own
generation at its next boot, instead of refusing until the rollout completes.
The play keeps its ordering (pre-stage behind the drain, rescue never
re-activates on exit 4) as defense in depth.

**One-time migration cost.** The first node to roll a release with this
change publishes every gated base that it has not cached and that is
`stale` or `legacy`.
- A base it reuses costs one upload.
- A base whose guest-init changed is rebuilt, which most releases already
  do.
- Later nodes find the bases `published`.
- ADR-20261024191249's budget (`join-node` 70 minutes) covers the worst
  case of every gated base.

**No extra node disk.** The cache keeps one entry per logical key, as
before. Shared storage keeps one set of keys per generation.

**Residual risks, not closed here:**
- **Same ref, different guest-init.** Two releases that configure the same
  ref share that generation's keys. Convergence still declines a
  publication built with another guest-init. But an N-1 cache miss gets N's
  bytes, and so N's PID 1, as before.
- **The producer catalogue's current pointer is still per logical key.**
  In a mixed fleet, alternating stages rebuild instead of reusing. That costs
  build time, never a boot. ADR-435 container conversions and runtime-default
  bindings can still refuse a base whose current producer belongs to the
  other release, as before.
- **The Debian parent stays on its logical key.** vmmd's
  parent-materialization allow-list accepts only that key. In a mixed fleet
  that changes the parent ref, parent rebuilds can alternate. Boots are
  unaffected, because children are standalone ext4.
- **Releases before this change still overwrite each other's logical key.**
  rc.294 and rc.296 can still flip it between themselves during the first
  rollout of a release with this change.
- **Generation keys accumulate.** Nothing collects them yet.

**New surface:**
- `storagekey.BaseGeneration` and `storagekey.BaseGenerationsPrefix`.
- `LocalCacheBackend` methods: `RouteGeneration`, `GenerationRoute`,
  `RoutesLocally`, `GetParent`, `RefreshFrom` and `PublishCached`, plus the
  package functions `storage.RouteGeneration` and `storage.GetParent`.
- `imaged.ConfiguredBaseGenerations`, `imaged.RouteBaseGenerations` and
  `Handler.RouteBaseGenerations`.
- The `legacy` adoption verdict.
- A `source` field (`generation` or `legacy`) on vmmd's
  `runtime base cache generation refresh completed` log.
- Startup log lines that list the routed bases.
- No migration, proto, metric, limit or Ansible change.

**Native acceptance owed before the next compute rollout:**
- On the internal test fleet, a two-node rollout of a generation-changing
  release in which the N-1 node stages after the N node's pre-stage, then a
  rollback of the N node.
- `make test-metal` and `make leakcheck`. vmmd's adoption path changed,
  though VM lifecycle did not.

The macOS development host cannot run these.

## Follow-ups

1. **Contract.** Once no release before this one can run or be rolled back
   to, stop reading the legacy logical keys and delete them. Collect the
   generation keys that no live or rollback-target release configures.
2. **Move the Debian parent to its generation's keys.** First vmmd's
   parent-materialization allow-list accepts generation parent keys (expand),
   then imaged stages the parent there.
3. **Key the producer catalogue's current pointer by generation.** This needs
   the ADR-595 SQL functions and the ADR-435 lineage to accept generation
   keys.
