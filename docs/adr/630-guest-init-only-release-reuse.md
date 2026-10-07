# ADR-630 · Releases that only change guest-init reuse staged bases and builds

- **Status:** proposed
- **Date:** 2026-10-06
- **Amends:** ADR-567 (runtime base convergence); the builder build cache
  identity in `pkg/builderd`. The two-drive layout (§4.6), ADR-005's cold-boot
  fallback and the base scan gate (issue #299) are unchanged.
- **Decision:**
  1. When a base's digest sidecar shows it was built from the current OCI config
     digest, source ref and base layout, and only the guest-init digest differs,
     imaged copies the staged ext4, replaces `/sbin/init` with `debugfs` (no
     mount, no root), and publishes the copy. Before publishing it checks that
     PID 1 holds exactly the new bytes, keeps its owner and mode 0755, and that
     `e2fsck -fn` passes. debugfs runs with a fixed `E2FSPROGS_FAKE_TIME`, so two
     nodes patching the same published bytes publish identical bytes. The
     patched artifact is signed, validated, rescanned and recorded exactly like
     a rebuilt one. Any refusal or failure falls back to the full rebuild.
  2. The builder build cache identity is the builder image's OCI config digest,
     the base layout, and `guestInitBuildContractVersion`. It no longer hashes
     the whole digest sidecar, which includes the guest-init binary digest and
     the source ref. A unit test hashes every guest-init declaration reachable
     from `runBuild` and `mountCgroup2`, and fails when that code changes. The
     author then bumps the contract version if the change can alter build
     output, and re-pins the digest.
- **Why:** The OCI bases are pinned by digest and have not changed since
  2026-10-01. guest-init links most of `pkg/api`, so its binary changes on
  every release. Its digest is part of both the base freshness key and the
  builder cache identity, so every release:
  - re-pulled, re-extracted and re-ran `mkfs` for all eight bases on every
    node. This took 228 s per node on production-us (2026-10-06, rc.243): 89 s
    for the 412 MB builder base, 12–33 s for each of the others.
  - started with an empty build cache. Every release-acceptance build and every
    customer's first build after a release took about 5 minutes instead of
    about 5 s.

  Because `mkfs.ext4 -d` output is not byte-deterministic, two nodes that
  rebuilt the same base also published different bytes. ADR-567 then had the
  losing node download the winner's copy, and ADR-510 refused cross-node
  restores until it did.

  Between 2026-09-22 and 2026-10-06, 28 commits touched `guest/init`. Only one
  changed the build-mode closure (`c1f9d08c9`, builder HTTPS preflights).
- **Consequences:**
  - A guest-init-only release reuses every base. Each base now costs one copy,
    a sub-second debugfs swap, the existing publish, sign and scan, and
    sidecars. On the slow-disk acceptance host, patching the 580 MB builder
    base took 15 s.
  - When nodes start from converged bytes, the patched publications are
    byte-identical, so ADR-567 has nothing to download.
  - Gate builds and customers' first post-release builds hit the cache unless
    the builder image, base layout or build contract changed.
  - The guard over-approximates by name. A same-named identifier pulls a
    declaration into the closure, which costs a false-positive re-pin, never a
    missed change. Changes outside `guest/init` (for example `pkg/api`'s
    `BuildManifest`) are covered by the existing `buildCacheRecipeVersion`
    rule for builderd.
  - Images that do not have the shape BuildBase produces are rebuilt as before:
    a non-regular `/sbin/init`, an `/sbin` that is not a directory or a fast
    symlink to one, or too little free space.
- **Validation:** Real-e2fsprogs unit tests cover `/sbin`, `/sbin -> usr/sbin`
  and `/sbin -> usr/bin` images, determinism (they fail without the fake time),
  and refusals. On the native KVM acceptance host, a base whose `/sbin/init` is
  busybox fails `TestMetalHelloBoot`. After the patch, the same base passes
  `TestMetalHelloBoot`, `TestMetalParkWakeCycle` and
  `TestMetalDNATPublishedToGuestPort`, and `leakcheck` is clean.
