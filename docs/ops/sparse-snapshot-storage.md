# Preserve snapshot memory holes

Snapshot memory streams contain large zero-filled ranges. Writing every byte
into the local snapshot store or read-through cache turns those ranges into
allocated disk blocks. This also prevents a sparse-memory restore backend from
skipping empty pages without scanning the entire file during wake.

The local store and both cache publication paths now recognize `snap/.../mem`
keys. They write nonzero data and seek over complete zero-filled pages, then
truncate to the original logical length before the existing sync and atomic
publication. Memory use remains bounded by a 256 KiB copy buffer. Other artifact
keys keep their existing copy path.

Readers, content hashes, registry uploads and snapshot format see identical
bytes. Sparse storage is not wire compression and does not reduce registry
egress. Cache budgeting continues to use the full logical length. Existing
dense cache entries are not rewritten by this change; future publication or
cache fills create sparse files.

Validation covers fragmented streams, unaligned/trailing zeros, interrupted
sources including wrapped EOF errors, cancellation, and preservation of a
previously published artifact after failure. Linux tests check allocated blocks
on the SSD XFS filesystem for local writes, cache writes and cache misses, as
well as eviction by logical byte size. The storage race suite also passes.

This change is useful preparation for anonymous-memory restore experiments.
It does not select a different Firecracker binary, enable huge pages, or by
itself establish the full-wake p95 below 350 ms requirement.
