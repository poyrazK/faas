# ADR-1336: Verified offline backups and restores for the owned object node

- Status: Accepted for the local prototype; no production enrollment.
- Date: 2026-10-16
- Related: [ADR-1331](1331-owned-object-storage-first-node.md), [ADR-1332](1332-owned-object-storage-write-receipts.md), [ADR-1333](1333-owned-object-storage-multipart.md), [ADR-1335](1335-owned-object-storage-scrubbing.md)

## Context

The node detects corrupt bytes through SHA256 scrubbing, but it cannot recover
from loss of its data disk. Copying a running directory can combine incompatible
catalog pointers, receipts and multipart state. Hashing already damaged source
bytes into a fresh manifest would preserve the damage without detecting it.
Recovery needs an independently verifiable, consistent artifact and a restore
procedure tested after the original directory has disappeared.

## Decision

Add `object-storagectl backup`, `verify` and `restore`, using shared offline
operations in `pkg/objectnode`. These commands do not contact the public gateway
or require node credentials. They operate on private operator-owned directories.
The live node format remains version 3. The backup format is separately versioned.

### Offline consistency boundary

Backup requires an existing, initialized format-3 directory and acquires the
same exclusive process lock as the daemon. It refuses a running node. Before
recovery, inspect the source tree with bounded depth and batched directory reads;
reject symlinks, special files and hard-linked regular files. Reject a non-regular
process-lock file before opening it. The directory's owner must prevent other
programs from changing its contents during the operation.

Perform ordinary startup recovery under the lock: settle pending write intents
before garbage collection, settle multipart receipts, expire old uploads, clean
abandoned staging/trash, and validate scrub state. The backup captures this
recovered state. It is not a forensic copy of an interrupted disk. No background
workers or listeners start, and the lock is held through backup publication.

### Backup format and verification

Write a private sibling staging directory on the destination filesystem:

```text
backup.json       version, engine, node format, UUID, timestamps,
                  entry/file counts, total bytes, manifest size and SHA256
manifest.jsonl    canonical JSON entry per directory or regular file;
                  relative path, type, size and SHA256
data/             recovered node tree, excluding its advisory .lock
```

Include empty directories, catalogs and current immutable blobs, all terminal
write receipts, active and frozen multipart sessions and selected parts,
multipart tombstones, scrub progress, its active worklist, and retained findings.
Do not copy obsolete bytes removed by ordinary recovery or historical object
bytes referenced only by receipts. Receipts remain proofs of publication, not
object versions. Preserve object identities, timestamps, headers and checksum
metadata through their original catalog records.

Stream one file at a time with a 256 KiB buffer. Check declared lengths and file
fingerprints before and after reading. Compare every object/part digest with the
stored catalog SHA256, and active scrub worklists with their saved digest.
Validate catalog relationships, record seals and terminal receipt states. Live
tracked objects must match their committed receipts, and multipart receipt
history must retain its matching terminal session. Each
copied file is fsynced. Write and fsync the manifest and completion record, then
read back and verify the complete backup before publication. Sync directories
bottom-up. Preserve the existing 1 GiB free-space floor on destination writes;
space checks remain observations rather than reservations against other tools.

Version 1 limits live in `pkg/api/limits.go`: at most 1,000,000 entries (including
directories), a 256 MiB manifest, and 4 KiB per manifest record. Verification
retains one bounded path set to reject duplicates and missing parents. The tree
layout is explicitly allowlisted; files outside the manifest, unsafe paths,
aliases, unsupported types, wrong lengths, damaged hashes and ambiguous JSON
fail verification. `verify` is read-only, including for expired upload sessions.

Hashes detect accidental corruption; they do not authenticate a backup whose
manifest and content an attacker can replace together. Backups contain customer
bytes and private metadata. Restrict access, protect transport and keep copies
in an independent failure domain. The first tool accepts filesystem destinations;
operators arrange the independent disk, mounted backup storage or transfer.

### Restore and durable publication

Both backup and restore destinations must not exist. Reject equal, nested and
ancestor source/destination paths after resolving parent symlinks. Copy a restore
into a private sibling directory, verify its actual bytes against the backup
manifest, and read back the restored copy. Then open it under the normal process
lock, run startup recovery, sync its tree and publish it with Linux
`renameat2(RENAME_NOREPLACE)` plus parent-directory fsync. Retain the restored
node's lock through publication. Never merge into or replace existing data.
Files use mode 0600 and directories 0700; advisory locks are recreated.

Multipart deadlines retain their original wall-clock timestamps. Restore may
expire an old upload during recovery; it never grants a new TTL. Scrub worklists
and findings retain their identities. A partial blob hash checkpoint compares
its saved device/inode/mtime/ctime with the restored file and restarts that blob
from offset zero when the fingerprint differs. Use the Go toolchain pinned in
the repository because scrub state includes serialized Go SHA256 state.

An error before rename leaves the final destination absent and cleans staging
on ordinary return. Process death may leave a private unpublished sibling;
future invocations never adopt it. A failure after rename, including parent
fsync failure or interruption before success output, can leave a complete final
directory with uncertain acknowledgement or durability. Never overwrite it on
retry: verify or inspect it before choosing a new destination. The CLI emits a
JSON `BackupInfo` only after successful publication/verification.

## Validation and boundaries

Tests remove the complete source directory before restoration and verify object
contents, empty buckets/objects, metadata, historical committed/rejected PUT
proofs, multipart completion receipts, active resume, frozen completion, expiry,
partial scrub checkpoints and retained findings. A pending durable PUT intent
is settled into the captured backup before later restoration.

Real subprocesses are killed during file copying, immediately before publication
and after durable publication, for backup and restore. Retrying a pre-publication
failure succeeds without adopting partial directories. Other cases include
destination creation races, I/O failure and cancellation boundaries, destination
damage after copying, missing/truncated/corrupt files, links, duplicate entries
and fields, traversal and refusal to back up a running or damaged source.

This milestone does not provide online snapshots, scheduled retention, remote
transfer orchestration, backup encryption, automatic repair, replication,
failover, sharding or a backup of the gateway's PostgreSQL admission state.
Coordinate control-plane recovery independently. In particular, this offline
snapshot cannot prove outcomes of writes accepted after its capture: fence
traffic and reconcile receipts/capacity before exposing a restored older node.
Read-only smoke checks and a successful isolated restore drill precede any
operator-controlled return to service. Replication and failover remain future
work; no production placement or provider enrollment changes here.
