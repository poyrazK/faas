# ADR-1337: Owned-node mutation journal and asynchronous follower catch-up

- Status: Accepted for the local prototype; no production enrollment.
- Date: 2026-10-16
- Related: [ADR-1332](1332-owned-object-storage-write-receipts.md), [ADR-1333](1333-owned-object-storage-multipart.md), [ADR-1336](1336-owned-object-storage-backups.md)

## Context

Verified offline backups recover a captured point in time. A second node needs
the changes made after that point, including deletes, bucket reincarnations,
multipart state and historical receipts. Current object bytes alone cannot
reconstruct that history: overwritten blobs and aborted parts are reclaimed.
Writing a change log after publication would leave a crash window in which an
acknowledged mutation was absent from the stream.

## Decision

Introduce an explicit offline `replication-enable` operation. An existing
format-3 node becomes a format-4 primary with a new immutable stream UUID,
genesis hash, and empty mutation journal. Ordinary nodes remain format 3.
Older binaries reject format 4 rather than ignoring its journal or follower role.

The layout is:

```text
replication/state.json          sealed role, stream, baseline and cursors
replication/log/<sequence>.json immutable sealed mutation records
replication/blobs/<sha>.data    retained immutable replay payloads
replication/transfer/<sha>.part unfinished follower transfers
```

Initialization prepares a complete directory in a recognized private staging
directory before publishing it. A crash before publication leaves only disposable
staging; a crash after publishing the empty primary state but before updating
the format marker is completed on startup only at sequence zero. Malformed
published state fails closed. Startup rejects links, special files, invalid
paths, missing journal records, broken chains and missing/wrong-sized retained
primary payloads. It never silently starts a new stream over missing history.

### Mutation ordering and acknowledgement

Capture each physical catalog write under the existing catalog lock. A record
binds its stream UUID, monotonically increasing sequence, previous record hash,
timestamp, exact relative path, canonical JSON metadata and optional blob
identity/size/SHA256. Directory-removal records represent object deletion,
bucket deletion and multipart part cleanup. Bucket recreation publishes its
new generation in the ordinary bucket record after the delete's sequence.

Before publishing a record, copy and fsync any required immutable payload into
the retained blob store and verify it against the original object/part SHA256.
Payloads are deduplicated by digest without hard links. Fsync the record and its
directory, persist the received position, apply the catalog mutation with file
and directory fsync, then persist the applied position. Only then does the
existing caller return success. The existing receipt protocol remains ordered:
intent, current-pointer publication and terminal proof are distinct records.

A durable record is a commit decision. If application or checkpointing fails,
retain the payload and fence catalog operations until replay succeeds. Recovery
adopts a final record published before its received checkpoint, replays it
idempotently, and completes the applied checkpoint before ordinary receipt,
multipart or garbage-collection recovery. A checkpoint acknowledgement lost
after rename is resolved from disk. Never overwrite a conflicting record or
rewind an applied cursor.

This is asynchronous replication. A primary response proves local journal and
catalog durability; it does not wait for a follower. Losing the primary disk
can lose acknowledged writes that the follower has not applied. A received or
applied physical position is not a new write-receipt proof, a quorum commit or
promotion authority.

### Snapshot boundary and follower fencing

The existing backup manifest includes the complete format-4 journal, retained
payloads and exact applied position. Require received equals applied at capture;
ordinary source recovery first settles any pending decisions. Verify that the
backup's advertised node format matches its actual marker. A metadata mismatch
cannot select the older, writable restore path.

`replica-bootstrap` restores a verified format-4 backup into a new directory.
The existing `restore` command also fences format-4 restores as followers. After
read-back verification, set the snapshot's applied position as the follower's
baseline, clear the copied historical journal/payload cache, and durably publish
the follower role. Only future records need transferring; baseline object bytes,
catalogs, receipts, multipart state and scrub observations remain intact.

A follower cannot open through `objectnode.Open` or run `object-storaged`.
Catch-up acquires the same exclusive process lock through a dedicated path and
does not run ordinary recovery, receipt settlement, multipart expiry, scrub,
garbage collection or an S3 listener. Expiry and all catalog changes must arrive
from the primary stream. There is no promotion command in this milestone.
The local role is an admission fence, not a distributed leader-election lease.

### Authenticated transfer and bounded catch-up

Reuse the loopback node's private operator SigV4 authentication:

- GET `/?gregale-replication=status` returns the primary's physical cursors.
- GET `/?gregale-replication=entry&stream=<uuid>&sequence=<n>` returns a sealed,
  applied journal entry.
- GET `/?gregale-replication=blob&stream=<uuid>&sha256=<sha>&offset=<n>&length=<n>`
  returns a bounded immutable payload range.

Reject wrong paths, methods, duplicate/unknown/mixed selectors, conditionals,
ranges and receipt selectors. Responses are `no-store`. An opted-out node returns
`ReplicationDisabled`. The client signs each request, validates content type,
length, stream/sequence/hash and range identity, follows no redirects and has no
fallback to ordinary object listing or GET. The primary remains loopback-only;
operators use an authenticated encrypted tunnel or their existing private TLS
boundary for cross-host access. Credentials stay in the existing node environment
variables and are never placed in flags or logs.

At each catch-up step, bind a primary head, verify the follower's current prefix
against that source, and receive the next exact record. Persist the received
position before downloading its payload. Save partial bytes with fsync, resume
from their on-disk offset, and verify the complete SHA256 before publishing the
retained blob. A checksum mismatch discards only the unpublished transfer and
returns an error. Replay does not execute old conditional requests again.
Apply the exact catalog effect and then advance the applied checkpoint.

Refuse foreign streams, divergent prefixes, gaps, duplicate old records in place
of the requested next record, regressing source positions and corrupt data.
Repeated complete catch-up does no work. `caught_up` means equality with the
head sampled at the beginning of that step; it is not a live availability claim.

Limits live in `pkg/api/limits.go`: 1,000,000 journal records, 2 MiB per record,
4 MiB per HTTP blob range, at most 32 applied records and 64 MiB transferred blob
bytes per step and a 30-second request timeout. The original one-second follow
polling is superseded by [ADR-1342](1342-owned-object-storage-replication-waits.md).
Metadata and local hash verification are additional I/O; a final payload check
reads its full contents. The existing free-space floor applies to retained
payloads, records, transfers and live replay copies.

`replica-catch-up` performs one bounded step; `--follow` repeats and retries
transient transport/I/O failures. Invalid configuration/authentication, fork,
schema and checksum errors stop the command. JSON progress exposes received and
applied positions, sampled source head, bytes transferred and catch-up state;
`replica-status` reads a stopped node's durable state under its process lock.
Continuous follow holds that lock and a narrow follower handle across waits,
validating retained history once at startup rather than rescanning it per step.
Existing disk metrics include retained journal storage in physical file usage;
it is never added to logical customer capacity or monthly billing.

### Retention and performance boundaries

Retain all records and payloads on the primary and all post-baseline received
records/payloads on the follower. No follower acknowledgement permits pruning in
this milestone. Orphan retained payloads from a failure before record publication
may remain; monitor disk use and keep complete backups. Do not manually prune
the journal. Hitting the record ceiling or free-space floor refuses further
mutation rather than silently dropping replication history.

Retaining and verifying a newly published blob performs additional disk I/O
while holding the catalog lock. A follower also needs space for retained payloads
and its live catalog copies. This first implementation trades throughput and
storage efficiency for an explicit recovery boundary. Segment compaction,
acknowledgement-based retention, publication pipelining and journal-specific
lag alerting require later qualification.

## Validation and remaining work

Tests cover ordinary and tracked writes, historical positive/negative receipts,
overwrites, deletes, bucket reincarnation, selected multipart parts, completion,
abort and expiry; snapshots at nonzero positions; format mismatch and restored
follower fencing; partial range resume, transfer budgets, partitions, corrupt
bytes, foreign/duplicate streams; and persistence failures before/after record,
received, catalog and applied publication. Real primary and follower subprocesses
are killed after durable records/catalog changes and recover through replay.
Authenticated HTTP and operator CLI tests exercise the complete catch-up path.

This milestone adds no synchronous write quorum, leader election, promotion,
automatic failover, sharding, production placement or control-plane database
schema. Fenced promotion, authority epochs and reconciliation of gateway state
must be implemented and qualified before serving traffic from a follower.
