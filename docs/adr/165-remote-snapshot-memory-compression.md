# ADR-165 · Remote snapshot memory compression

- **Status:** proposed
- **Date:** 2026-09-07
- **Decision:** Encode Firecracker snapshot memory as Zstandard only in the
  authoritative OCI registry while retaining uncompressed sparse files in
  every node-local cache.
- **Why:** A fresh 1 GiB snapshot on the production SSD node spent about 6
  seconds in Firecracker capture and local sparse staging, then about 29
  seconds uploading the uncompressed memory file. The exact Go encoder used by
  this change compressed that file to 24,608,163 bytes in 1.04 seconds with
  one worker and about 12.5 MiB peak RSS.
- **Consequences:** Snapshot publication and replica prepositioning transfer
  substantially fewer bytes. Local Firecracker restores keep their current
  file representation and do not decompress on the wake path.
- **Rejected alternatives:** Asynchronous publication without a durable
  publisher state, compression inside the local cache, and compression of
  every artifact.

## Context

ADR-063 makes the shared OCI registry the authoritative transport for snapshot
memory and vmstate. `LocalCacheBackend` keeps the restore-ready copy on each
compute node. Sparse snapshot writes remove physical zero blocks from local
XFS, but a registry upload still sends every logical byte. On the measured
Scale-plan snapshot this meant sending 1 GiB even though only about 128 MiB of
the sparse file had allocated blocks.

The current publication is synchronous. Making the upload asynchronous would
need a durable publication state and recovery protocol before imaged could
advertise the snapshot safely. Without that protocol, a process restart could
publish a snapshot row whose remote memory object is absent. Compression
reduces the existing synchronous work without changing snapshot ownership or
publication ordering.

## Decision

`OCIRegistryStorageBackend` supports two snapshot-memory representations:

- Legacy manifests have no encoding annotation and their layer contains the
  original bytes.
- Compressed manifests set `dev.gregale.storage.encoding=zstd` and
  `dev.gregale.storage.uncompressed-size=<bytes>` on the layer descriptor.

Readers always accept both representations. Writers use the legacy
representation by default. Operators opt in with
`FAAS_STORAGE_SNAPSHOT_COMPRESSION=zstd`; `none` and an unset value preserve
legacy writes. Unknown configured or manifest encodings fail closed.

Compression applies only to valid `snap/.../mem` keys, including immutable
init and warm capture keys from ADR-159. Snapshot vmstate, app layers, base
images, kernels, signatures, scans, and sources retain their existing bytes.
The encoder uses the fastest Zstandard level with one compression worker per
capture so simultaneous parks do not each claim all host CPUs.

The production wrapper order remains `LocalCacheBackend` over the OCI backend.
On Put, the cache first writes the original memory stream as a sparse file and
the OCI backend compresses a separate temporary remote representation. On a
replica cache miss, OCI verifies the compressed blob digest, decodes it, and
the cache materializes the original sparse memory file before marking the
replica ready. A prepositioned wake therefore reads an ordinary Firecracker
memory file and never decompresses during restore.

## Rollout and rollback

This is a reader-first wire-format rollout:

1. Deploy the reader-capable release to every compute node with the setting
   unset or `none`.
2. Verify legacy snapshots still restore on each node.
3. Set the value to `zstd` fleet-wide and create a fresh snapshot.
4. Verify the origin cache, replica cache, restore, and cold-boot fallback.

Rollback first sets the writer value to `none`. Reader-capable binaries must
remain on every node while compressed snapshot manifests exist. Snapshots are
disposable caches, so an emergency rollback to an older reader also requires
invalidating compressed snapshots through the owning services and allowing
cold boot to rebuild them.

## Consequences and limits

Publication spends bounded CPU and temporary disk on a compressed OCI copy.
The measured memory shape strongly favors this trade: 24,608,163 bytes cross
the registry boundary instead of 1 GiB, a 43.6× reduction. Replica cache misses
pay decompression during asynchronous prepositioning or on-demand fallback;
prepositioned wake latency is unchanged.

Compression improves snapshot creation, distribution, and deploy readiness.
It does not reduce Firecracker restore time for a snapshot already present in
the SSD cache, and it does not by itself guarantee the public edge p95 target.
