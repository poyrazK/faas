# ADR-1135: Sparse extent encoding for remote drives and snapshot memory

- **Status:** proposed
- **Date:** 2026-10-14
- **Builds on:** ADR-165 (remote snapshot memory compression), ADR-633
  (block sharing), ADR-005 (cold boot must always work)

## Context

App drives are padded to the plan cap, so a Scale app's drive is a 2 GiB ext4
image that may hold 90 MiB of files. The GCS backend already stores drives,
rootfs images and (with `FAAS_STORAGE_SNAPSHOT_COMPRESSION=zstd`) snapshot
memory as one fast zstd frame of the logical bytes. The object is small: a
2 GiB production drive is a 35–124 MB object.

A node-cache miss still pays for the logical size. The node decodes 2 GiB of
zstd output, xxhash-checks it, and scans every 4 KiB page for zeros while
writing the sparse cache file. A CPU profile of that path splits roughly into
55% write syscalls, 23% zstd sequence decoding of the zero runs and 5%
checksum. The fastest zstd level stores long zero runs as matches, so even the
zeros are copied byte by byte.

Production measurements (2026-10-13/14, read-only):

- compute-1 vmmd materialized the `main` drive 9 times in 16 h, each 2 GiB
  logical, 4.1–10.8 s per fill (`vmmd_snapshot_materialize_seconds`).
  Snapshot memory misses averaged 3.0 s for ~0.94 GB logical.
- Those fills are the whole tail of `resolve_images`: 76 of 85 restores
  resolved in ≤1 ms, 9 took over 2 s.

## Decision

Add a second remote encoding, `zstd-extents`, used for the same keys the GCS
backend already compresses (drives, `apps/` and `jobs/` rootfs images, and
snapshot memory when memory compression is on).

The object body is one zstd frame whose content is a header followed by the
bytes of each data extent in file order:

```text
magic "GRGEXT1\n"
uvarint logical size
uvarint extent count
count × (uvarint gap from the previous extent's end, uvarint length)
```

- **Writer.** When `FAAS_STORAGE_SPARSE_EXTENTS=1` and Put receives a regular
  file read from its start, the backend lists the file's data ranges with
  `SEEK_DATA`/`SEEK_HOLE` and compresses only those. The node cache already
  hands the parent its sparse spool file, so cache-fronted writes qualify. A
  stream that is not a file keeps the plain zstd encoding. On a filesystem that
  does not report holes the whole file is one extent, so the encoding stays
  correct without saving anything.
- **Reader.** The GCS backend always accepts `zstd-extents`, whatever the
  writer setting. The logical size in the header must equal the object's
  `gregale-storage-uncompressed-size` metadata. Extents must be ascending,
  non-empty and inside the logical size. The decoded stream must end exactly
  after the last extent, which also makes the decoder check the frame
  checksum. Anything else fails closed.
- **Node cache fast path.** The reader writes each extent at its offset and
  truncates the file to the logical size, leaving the rest as holes. The cache
  takes this path for any parent reader that offers it. Every other consumer
  reads the logical byte stream, with zeros produced for the holes.

The extent map lives inside the compressed frame, not in GCS custom metadata,
because metadata is limited to 8 KiB and an ext4 image can have thousands of
extents. A header may list at most 2^20 extents.

## Rollout and rollback

This is a reader-first wire-format change, like ADR-165:

1. Deploy the release to every node that reads the bucket, with
   `FAAS_STORAGE_SPARSE_EXTENTS` unset.
2. Set `FAAS_STORAGE_SPARSE_EXTENTS=1` on the writers and publish new drives and
   snapshots.
3. Check `vmmd_snapshot_materialize_seconds{source="materialized"}` on the
   compute nodes for the next cache misses.

Rollback unsets the writer flag. Objects already written in the new encoding
need reader-capable binaries until they are replaced. Drives are rewritten on
the next deploy, and snapshots are caches that cold boot can rebuild
(ADR-005).

## Consequences

- On n2 (GCE, 2 CPUs), a cache miss of a 2 GiB drive with 120 MiB of random
  data took 2.9–4.2 s in the plain encoding and 1.1–1.4 s in the extent
  encoding. Publishing the same drive took 2.8–2.9 s and 0.8–1.0 s. Real drive
  contents compress, unlike the benchmark's random data, so both encodings do
  better on them; the plain encoding's cost stays tied to the 2 GiB.
- The object size barely changes, so download time does not move. The saving
  is local CPU and disk work on the miss and the publish path.
- `TestCacheMissMaterializeBench` (opt-in, `FAAS_STORAGE_MISS_BENCH=1`) measures
  both encodings on any host.

## Not in this change

- **OCI registry backend.** It keeps the ADR-165 encodings. Production uses
  GCS, and an OCI deployment never reads GCS objects.
- **Exclusive artifacts** (`gcs_exclusive.go`). They use their own receipt-bound
  zstd stream and keep it.

## Rejected alternatives

- **Store drives unpadded and extend them at staging time.** The ext4
  filesystem inside is built for the padded size, and resizing it on every
  restore adds work to the wake path.
- **Parallel zstd decoding.** It spreads the same logical-size work over more
  cores that the node's guests need.
- **Extent map in GCS metadata.** Metadata is capped at 8 KiB.
