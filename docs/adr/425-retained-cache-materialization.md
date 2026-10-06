# ADR-425 · Retain opened cache artifacts during VM materialization

- **Status:** proposed; native KVM acceptance pending
- **Date:** 2026-10-01
- **Decision:** cache Get readers optionally implement `LocalFileLinker`.
  VMMD can create an instance-owned hardlink to the exact opened inode instead
  of copying the already materialized artifact again. The cache verifies the
  destination inode against the reader, including when another daemon replaced
  the cache path. Eviction and reader close cannot remove the retained name.
  VMMD removes it through the existing materialization teardown. Failed links
  use the opened reader's copy path, without fetching the object again.
- **Why:** a remote cache miss first writes the decoded artifact into the local
  cache; VMMD then copies it into TMPDIR. Sparse memory/disk files incur extra
  extent work even on XFS, where dense copies can already use reflinks through
  `copy_file_range`. A production-filesystem microbenchmark on October 1 found
  a 256 MiB sparse kernel copy at median 14.33 ms versus 0.12 ms for a verified
  hardlink (10 samples each). A dense 128 MiB copy was already 0.077 ms versus
  0.073 ms. These are filesystem operations, not full restore measurements.
- **Consequences:** no new cache pin, quota, routing, placement or wire policy.
  Retained artifacts are immutable inputs; writable drives still use private
  copies/CoW clones. Cache eviction may remove its name while the instance owns
  the same blocks, as ordinary instance materializations already do. Both
  retained and oversized temporary readers support this capability. Cross-device
  paths, eviction before linking, or replacement before linking retain the
  existing copy behavior and cold-boot fallback. `materialized` telemetry still
  means a cache miss, even when the second copy is eliminated.
- **Rejected alternatives:** probing LocalPath again after Get (an unowned path
  can be evicted or replaced before staging); linking the pathname without inode
  verification (can select a newer artifact than the opened stream); sharing a
  writable drive inode (violates instance isolation); retaining paths through a
  new cache lease manager (adds lifecycle state when a hardlink suffices).

## Scope and validation

The measured September 30–October 1 cohort spent a mean 10.82 seconds resolving
memory, VM state and drive artifacts on materialized restores. This optimization
removes only the redundant local operation. Remote transfer, decompression,
cache fsync and sparse scanning remain; the microbenchmark does not establish a
new platform restore latency or predict seconds of improvement.

Unit tests cover no second read on a cache miss, oversized artifact retention,
eviction, replacement, unchanged stream position, occupied destinations, copy
fallback, writable disk isolation and instance teardown. The Go benchmark keeps
the old reader's `WriteTo` kernel-copy capability for a fair local comparison.
Before release, run `make test-metal` and `make leakcheck` on the dedicated
native x86_64 Linux KVM acceptance host; nested production compute is not an
acceptance substitute.
