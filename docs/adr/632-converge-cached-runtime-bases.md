# ADR-632 · Converge every cached runtime base, not only the ones this daemon staged

- **Status:** proposed
- **Date:** 2026-10-07
- **Amends:** ADR-567 (runtime base convergence). ADR-510's restore identity
  check and ADR-005's cold-boot fallback are unchanged.
- **Decision:** each ADR-567 convergence pass (at start-up and every minute)
  also considers the minimal base and every runtime in
  `DefaultRuntimeBaseRefs` whose base key is cached on this node, even when
  this imaged process has not staged it. The recipe for such a base is the
  ref this daemon would build it from (`resolveDeployBaseRef`: the
  digest-pinned env override, else the default). If that ref cannot be
  resolved (a named node refusing an unpinned default), the cached copy is
  left alone. `convergeBase` is unchanged: the copy is replaced only by a
  publication of that same source ref built with this daemon's guest-init.
- **Why:** ADR-567 tracked only bases that `EnsureBaseExt4` returned for in
  the current process. Runtime bases are staged lazily: at start-up imaged
  stages the builder base, the minimal base and the node's assigned
  runtimes, and every other runtime only when a deployment of it lands on
  the node. A copy cached by an earlier daemon therefore kept whatever bytes
  that daemon built. vmmd still attaches it, because the cached generation
  marker records the OCI ref, not the bytes.
  On production-us on rc.243 (guest-init `f6133695`), the GCS publications
  and the two compute nodes disagreed as follows:

  | Base | Publication | compute-1 | compute-2 |
  |---|---|---|---|
  | `python312` | `9683015d` | matches | `8b3dcb6c` (guest-init `681ff705`) |
  | `go124` | `c3690c64` | matches | `3dc004f8` |
  | `python313` | `0d7b37f2` | `aefd859a` | matches |
  | `node22` | `11f37484` | matches | matches |

  imaged logged convergence only for the builder, minimal and node22 bases.
  Over 24 hours vmmd refused about 137 cross-node restores with
  `snapshot backing images changed`. Each wake cold-booted and recaptured a
  snapshot that the other node then refused again.
- **Consequences:**
  - Every base on a node's disk converges within a minute of imaged
    starting, whichever daemon cached it.
  - A divergent node pays one base download per divergent runtime. Snapshots
    captured against its old copy are refused once (ADR-510), so each
    affected app pays one cold boot.
  - In steady state there are two small parent sidecar reads per cached
    base per minute, as in ADR-567, for up to eight more bases.
  - Execution-profile bases are staged at every start and need no seeding.
- **Rejected alternatives:**
  - Stage every runtime eagerly at start-up: this is the lazy-staging
    decision `EnsureRuntimeBase` documents, made to protect a new node's
    readiness budget and memory. Convergence only downloads what is already
    cached.
  - Deterministic `mkfs.ext4 -d` builds: a fixed `-U`, `-E hash_seed` and
    `E2FSPROGS_FAKE_TIME` make rebuilds byte-identical on one node, but
    source ctimes still differ across nodes (measured on rc.243), as ADR-567
    already found.
  - Converge from the local `.digest` sidecar's source ref instead of the
    resolved ref: a stale sidecar describes exactly the old recipe this
    repairs, and it would let a node adopt a publication it would not build.
