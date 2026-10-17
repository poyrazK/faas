# ADR-1551: Owned-node journal retention and compaction

- Status: Accepted for the local prototype; no production enrollment.
- Date: 2026-10-16
- Related: [ADR-1543](1543-owned-object-storage-write-receipts.md), [ADR-1544](1544-owned-object-storage-multipart.md), [ADR-1547](1547-owned-object-storage-backups.md), [ADR-1548](1548-owned-object-storage-replication-journal.md), [ADR-1549](1549-owned-object-storage-primary-switchover.md), [ADR-1550](1550-owned-object-storage-replica-durability.md)

## Context

The primary and followers retain replay records and payloads after they cease to
be current object data. The original million-record lifetime sequence ceiling
eventually stops every continuously used stream. Deleting files without an
anchored floor would break chain validation, replication and historical write
recovery. Only the designated durability replica currently reports its cursor;
other asynchronous followers have no retention registration.

## Decision

Add operator-triggered, offline `object-storagectl compact`. Its exclusive process
lock excludes the daemon, streaming requests and follower workers. There is no
online compaction endpoint, automatic cleanup or force-prune option. Preserve
all customer object data, historical receipts, multipart sessions and tombstones.
Compaction removes only obsolete journal records and replay payloads.

### Separate sequence identity from physical retention

Continue monotonically increasing sequence numbers across compaction and planned
promotion. Bound them at 2^53-1 for exact JSON integer representation. Keep
20-digit journal filenames. Separate publication budgets are one million retained
records, 1 GiB of journal files and 8 TiB of replay payloads, with at most one
million payload files. Existing record, range, per-step, object and free-space
limits remain. All constants live in `pkg/api/limits.go`.

Maintain physical usage in memory under the catalog lock, count uncertain
published records and orphan payloads, and reconstruct counts on startup. Charge
a deduplicated replay payload once. Refuse publication past a retention budget;
never fall back to an unjournaled write. In-flight transfer files remain bounded
by the existing one-received-record and object-size rules. Retention metrics are
cached operational observations, not capacity settlement or billing evidence.

### Explicit follower retention promises

Add offline `replica-register` and `replica-retire`. Registration uses the intended
follower's node UUID, applied sequence and hash from its stopped `replica-status`.
The primary verifies that exact retained position and saves a fresh registration
UUID. An identical current enrollment is idempotent. The trusted operator owns
identifying the actual disk; the source hash is not remote disk attestation.
At most 64 followers are registered, sorted canonically by node UUID.

Registered workers report their already fsynced applied position after each
successful bounded step, including no-work retries. Add an optional
`FollowerReporter` capability and private SigV4 POST
`/?gregale-replication=cursor`. Bind version, stream, primary, epoch, registration
UUID, node UUID, sequence and hash in canonical JSON. Require a signed SHA256
body, the existing 4 KiB report ceiling and exact selectors. Validate identities,
monotonicity and the hash against retained records or the floor. Persist and fsync
before acknowledging; reread/sync durable state on retries after uncertain
publication. Never publish a speculative memory cursor after a failed save.

An unavailable follower keeps its pin until explicit retirement. Retirement does
not alter a separate replica acknowledgement policy. Re-enrollment creates a new
registration UUID, rejecting stale reports from the previous enrollment. Unknown
followers can use available history without a retention guarantee. These reports
do not establish write authority, satisfy replica acknowledgements, or fence a
primary. Existing authentication and encrypted cross-host transport requirements
remain unchanged.

### Durable floor before deletion

Use sealed replication state version 4, retaining older state readability and
rejecting it in older binaries. Add an optional floor checkpoint containing
version, sequence, hash, creation time and a pruning-in-progress marker. The
checkpoint matches `BaseSequence`/`BaseHash`, which now identify either a verified
snapshot baseline or a compacted physical prefix. A primary can advance beyond
its original baseline and promotion boundary only with this checkpoint. Retain
the sealed promotion certificate and normal epoch validation above the floor.

Select the minimum of the local applied head, all registered applied cursors,
and the designated replica's confirmed sequence. Unknown replica confirmation
pins the existing floor. An explicit smaller `--through` is allowed; a cut past
any pin is refused. Active replica confirmations therefore always retain an
exact record or floor proof. Drop obsolete local-mode confirmation telemetry when
the new floor passes it; no active replica promise changes.

Before checkpoint publication, validate the retained chain, vet file types and
hard links before following catalog paths, and hash current referenced data and
replay payloads. Seal/fsync the new floor with pruning marked pending. Only then
unlink records at or below the floor. Keep every payload referenced by a retained
record or a pending receipt, deleting the remaining replay files. Fsync both
directories before sealing the completed marker.

Startup accepts obsolete files below the floor only while pruning is recorded.
It validates all required retained records, authority, confirmations and registered
cursors before continuing cleanup. Cleanup is idempotent and resumes after a
kill, cancellation or lost acknowledgement. Required missing history always
fails closed. A completed checkpoint without durable deletions cannot be
published. The operator must not roll back state or copy old files over a node
after an uncertain compaction result.

### Preserve multi-record recovery

Compaction does not run ordinary primary receipt settlement, expiry or physical
object garbage collection. It first resolves any received journal decision, then
preserves the physical catalog as it stands. This allows the command to free a
retention ceiling reached between a receipt's intent, object publication and
terminal proof. Pending receipts explicitly retain their replay payload even
when their intent record is below the floor. Current object/part data is a
separate copy and remains untouched. Completing multipart sessions and terminal
historical proofs likewise remain in place. Normal primary startup then settles
remaining decisions under the persisted durability policy; later compaction can
reclaim newly obsolete payloads.

Follower compaction uses the same process lock and requires received == applied;
a partial transfer must finish first. It preserves the follower role and all
copied recovery state. Fenced sources refuse new compaction to preserve the final
handoff export. Offline replica replacement can still update its policy without
ordinary settlement; a downgrade to local mode must first recover and confirm
the complete tail, as before.

### Snapshots, rejoining and observations

Backups validate the floor and retained chain and reject pending cleanup markers.
Normal startup resumes cleanup before an offline backup is captured. Verified
restore/bootstrap assigns a fresh follower identity at the snapshot's applied
position, clearing copied registrations, checkpoint maintenance state and
acknowledgement authority. A successor can compact after planned promotion while
retaining its handoff certificate and sequence history.

A follower below the source floor gets `ErrReplicaReseed`, surfaced as HTTP 410
`ReplicaReseedRequired` and a permanent CLI error. It must bootstrap a new
directory from a fresh verified snapshot; there is no cursor reset or skipped
history. A follower at the floor can continue, including idempotent cursor and
confirmation reports without old replay data.

Export aggregate floor, applied/safe sequence, pinned records, registration count,
retained records, journal bytes and replay bytes. Metric callbacks perform no
disk I/O and have no high-cardinality identifiers. Operators schedule offline
maintenance before a limit or disk floor is reached. Current objects, receipts,
multipart tombstones and backups retain independent capacity requirements.

## Consequences and validation

Replay history is reclaimable without recycling sequence numbers or discarding
gateway settlement evidence. Explicit registrations make offline followers an
operator-managed retention commitment. Compaction needs downtime, hashes data,
and retains a bounded map of replay digest references. Shared payloads cannot be
deleted while a retained record or pending receipt still needs them.

Validation covers shared payloads, pending receipt recovery, retained negative
and overwritten/deleted proofs, active/terminal multipart state, partial follower
transfers, pin refusal, retirement and re-enrollment generations, delayed and lost
reports, uncertain cursor persistence, replay budgets, sequences beyond the old
ceiling, compacted backup/restore and promotion beyond the authority boundary.
Subprocess kill tests cover floor publication, partial record/payload deletion,
directory fsync and completion; missing retained records, damaged current bytes,
symlinks and hard links fail before unsafe cleanup. Authenticated HTTP and CLI
checks cover cursor reports, explicit reseeding, cached metrics and process locks.

Automatic retention, online compaction, receipt/tombstone garbage collection,
publication pipelining, quorum authority and production enrollment remain separate
work. There is no new SQL schema or public tenant API.
