# ADR-1554: Prepare replay payloads outside catalog publication

- Status: Accepted for development qualification; no production enrollment.
- Date: 2026-10-16
- Related: [ADR-1543](1543-owned-object-storage-write-receipts.md), [ADR-1544](1544-owned-object-storage-multipart.md), [ADR-1548](1548-owned-object-storage-replication-journal.md), [ADR-1550](1550-owned-object-storage-replica-durability.md), [ADR-1551](1551-owned-object-storage-journal-compaction.md), [ADR-1552](1552-owned-object-storage-local-qualification.md), [ADR-1553](1553-owned-object-storage-replication-waits.md)

## Context

Change-driven catch-up increased replica throughput, but matched local runs also
showed lower foreground throughput and variable CPU and tail latency. A separate
controlled profile exercises 24 distinct 5 MiB tracked writes with four parallel
workers and a concurrent reader, with and without a follower on the same host.
In its baseline profile, journal publication accounts for 49% of cumulative CPU
samples. Replay copying and verification run under the catalog write lock;
nearly all sampled mutex delay is attributed to `Put` publication. Reader maximum
latency is 57–113 ms. These short profiles identify work to move, not the sole
cause of all prior measurements or a production performance objective.

## Decision

### Request-scoped preparation

For PUT, part upload and multipart assembly, prepare replay data after the
upload/assembly stage is fsynced and before acquiring the catalog write lock.
One preparation slot per store bounds extra copying and temporary duplication;
queueing is cancelable and holds no catalog lock. Keep the lifecycle read lock
so fencing and close drain the operation, as during the existing upload.

If the retained SHA256 payload is absent, copy and verify into a fresh UUID file
in staging. Otherwise verify both staged input and retained bytes. Use fixed
buffers, exact lengths, the existing free-space floor, regular-file/single-link
checks, and before/after fingerprints. Preparation grants no commit, receipt,
capacity or authority proof.

Under the publication lock, run existing recovery and recheck generation,
conditions, multipart state, quotas and cancellation. Bind the preparation to
this request's exact object/path, size and SHA256. Recheck device/inode, length,
mtime and ctime before moving the live file; after the known rename accept only
its ctime change and bind the new fingerprint. Reject replacements, ordinary
writes, symlinks and hard links.

Recheck live and prepared/retained files before journal publication. Install new
retained data without replacement, account for it under existing replay quotas,
and fsync both destination and staging directories before a journal decision.
Within that same locked operation, receipt/current replay can use the freshly
verified immutable file proof rather than copying/hashing payloads again.
Receipt intent, catalog publication, terminal proof and applied confirmation
retain their existing durable ordering. Metadata and directory fsyncs remain
serialized.

Proofs are never persisted or reused across requests or restarts. Startup replay,
uncertain prior decisions, follower application and offline integrity checks
retain their full byte verification paths. Fingerprints establish freshness of a
just-computed digest, under the existing exclusive process/private filesystem
ownership contract. They are not persistent integrity proofs; external concurrent
modification is unsupported.

Cancellation, generation changes, abort or conditional rejection remove unused
staging copies without consuming retained replay quota. A retained install with
an uncertain later fsync/journal acknowledgement keeps conservative accounting
and the existing recovery rules. Staging remains non-authoritative and is cleaned
on restart. No disk-format, durable-intent or SQL change is needed.

### Bounded catch-up work

Keep byte-work and entry-start budgets across bounded steps and reconnects.
CLI defaults are 64 MiB/s accounted byte work and 256 entries/s; maxima are
1 GiB/s and 4,096 entries/s. Bursts are one existing 4 MiB range and one existing
32-entry batch. Limits live in `pkg/api/limits.go`.

Account for requested network ranges, downloaded-file hashing, retained cache
reads, and live replay copy/verification reads. Reused payloads cannot bypass
pacing. Reservations conservatively include failed/short reads and EOF probes.
Waits cancel safely; partial ranges resume and only fsynced applied positions
can be reported. Slower pacing can increase lag and response uncertainty, which
must retain existing receipt/capacity holds.

Expose `--work-bytes-per-second` and `--work-entries-per-second`, requiring positive
bounded CLI rates. Existing library callers may leave both new options zero for
their explicit unpaced behavior. A restart or changed library options creates
fresh bursts. Progress adds `work_bytes` and `work_wait_ms` for each bounded step.
Actual payload throughput can be below the work rate because bytes require
multiple reads/copies. This is a per-worker work budget: startup integrity
validation, offline maintenance, foreground uploads, metadata/fsync cost and
OS-level CPU/disk isolation are outside it.

Each daemon handler separately admits two active payload responses; saturation
returns the existing authenticated 503 capacity response. Status, confirmations,
cursor reports and change waits remain independent. Existing 4 MiB ranges bound
in-flight payload volume. Aggregate active-transfer and rejection metrics add no
stream, node or object labels.

## Validation and consequences

Tests require reads/metadata work during a blocked preparation, bounded queue
cancellation, generation/condition races, part abort and frozen completion,
changed files with restored mtime, and unsafe aliases. Child processes are killed
after preparation, after retained installation and after journal publication to
verify the existing commit boundary, receipts and follower recovery.

Budget checks cover retained cache reads, tokens surviving bounded steps,
resumable cancellation without premature confirmation, CLI controls/progress,
independent bounded HTTP transfers and slot release on failed response writes.
Run existing crash, multipart, receipt, compaction and durability regressions,
repeat the publication profile, and qualify matching mixed gateway workloads in
both durability modes. Keep adverse measurements alongside improvements.

Catalog operations can progress during preparation, but payload work still
serializes at its separate slot and publication still serializes metadata/fsyncs.
Independent-host/disk qualification, resource isolation, automatic compaction
and production enrollment remain separate work.
