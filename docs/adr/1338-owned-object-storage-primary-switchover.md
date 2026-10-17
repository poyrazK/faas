# ADR-1338: Planned owned-node primary switchover

- Status: Accepted for the local prototype; no production enrollment.
- Date: 2026-10-16
- Related: [ADR-1332](1332-owned-object-storage-write-receipts.md), [ADR-1333](1333-owned-object-storage-multipart.md), [ADR-1336](1336-owned-object-storage-backups.md), [ADR-1337](1337-owned-object-storage-replication-journal.md)

## Context

Asynchronous catch-up preserves ordered mutations and receipts. It does not
establish exclusive write authority: a follower matching a sampled head cannot
prove that the primary has stopped writing. Restart, lost acknowledgements and
two promotion candidates must not produce two writable nodes. Gateway admission
and receipt recovery also live outside the node's replicated data directory.

## Decision

Support a planned handoff from a reachable, authenticated primary. Introduce
replication state version 2 with a durable node UUID, current primary UUID,
positive authority epoch, optional previous promotion grant and optional outgoing
handoff. The journal retains its stream UUID and monotonically increasing
sequence/hash chain across handoffs. New version-2 records include their epoch
in the sealed hash. The successor continues after the sealed final sequence;
promotion does not reset retention or the journal's existing record ceiling.

Existing version-1 state is upgraded under its process lock. Primaries acquire
an identity at epoch 1; legacy followers acquire an independent identity and
bind the primary identity only after authenticated hash-prefix verification.
Version-1 records remain readable as epoch 1. Old replication binaries reject
version-2 state/records rather than ignoring authority. Format 4 continues to
exclude pre-replication binaries. Upgrade both participants before handoff.

### Fence the source before issuing authority

`primary-fence --data-dir FOLLOWER --source OLD_PRIMARY` owns the follower's
process lock, checks its source binding and verifies the candidate's current
catalog and all baseline/live/retained data. Recognized unpublished `.tmp-UUID`
files left by interrupted publication are removed under that lock with parent
fsync; unsafe types, aliases and unknown paths fail closed. No catalog decisions
or expiry run on the follower. An unfinished or damaged candidate
fails this preflight before the primary is asked to fence. Stop continuous
catch-up before running this command. Catch up again first if a transfer or
physical receipt transition is incomplete.

The private SigV4 POST `/?gregale-replication=fence` binds `stream`, `primary`,
`epoch` and `target` query parameters. Empty-body verification and existing
strict private-query validation apply. The source acquires the exclusive
lifecycle lock, draining uploads and multipart assembly that stream outside the
catalog lock while blocking later operations. Under the catalog lock, it
finishes durable journal/receipt decisions and multipart settlement, then
atomically writes and fsyncs a sealed handoff and `fenced` role together.

The grant binds its UUID, source/target UUIDs, stream, old epoch, final sequence
and hash, and creation time. The final checkpoint cannot move afterward. A
retry for the same source identity, epoch and target returns that exact grant;
another candidate or stale epoch is refused. There is no unfence or retarget
operation. An uncertain publication quarantines ordinary service in the live
process. A retry rereads durable state and confirms directory fsync before
acknowledging an existing fence.

Fenced startup skips all ordinary receipt, multipart, expiry, garbage collection
and scrub recovery. It can reopen only as an export of its sealed stream.
Ordinary S3, private inventory/receipt requests and local mutations are refused
with `PrimaryFenced`; authenticated replication status/entries/blob ranges remain
available so the target can finish catch-up. Background maintenance stops.
An uncommitted fence can leave the old primary writable after restart; no grant
was confirmed in that case and promotion still requires the live durable fence.

### Verify and promote one target

`replica-catch-up` accepts a fenced source at the same authority epoch. It still
requires matching source identity, stream, prefix and entry epoch. Other
followers may copy the final position but receive no promotion authority.

`replica-promote --data-dir FOLLOWER --source OLD_PRIMARY` requires an
authenticated live source response confirming its durable fence for this exact
node. Received and applied sequence/hash must both equal the sealed checkpoint.
Adopt/check any entry published before a lost received checkpoint and refuse
pending transfers or unresolved receipt/catalog transitions. Verify all catalog
references, terminal historical receipts, multipart state, baseline/current
object and part hashes, and retained journal payloads before changing authority.
Walk/file-count limits and the fixed streaming buffer reuse the backup limits.
The checks are cancellable and leave the follower fenced on failure.

Publish the primary role, the target's primary identity, epoch plus one and
the original sealed grant in one atomic, fsynced state replacement. Catalog
bytes and object/receipt identities do not change. Repeating a completed
promotion acknowledges its durable local grant without incrementing the epoch
again, including when its response was lost and the source is now unavailable.
A first promotion always needs a reachable source. The operator starts the
successor daemon separately; promotion itself opens no listener.

Startup and backups validate the grant's boundary against retained history or
the verified snapshot baseline. Subsequent entries must use the new epoch.
Followers bound to the old authority refuse the successor instead of accepting
an implicit change of writer. They require a verified fresh successor snapshot.
Format-4 restore/bootstrap continues to assign a fresh follower identity and
never clones a primary. The old directory remains fenced; rejoin uses a new
directory seeded from the successor's backup, retaining the old fenced copy.

### Gateway cutover and accounting

Pause customer mutations/admission and background object mutations before
fencing. After promotion, start the new daemon privately and move a stable
backend tunnel/routing target while retaining the configured backend identity,
namespace, physical bucket names, endpoint and receipt protocol. Changing an
endpoint or placement fingerprint is not a cutover: existing placements and
saved receipts are deliberately fenced by registry validation.

Keep customer writes paused while existing apid recovery confirms saved PUT
and multipart receipts through the successor. Scan coherent inventory and
verify committed bytes/keys and multipart state. Missing proof remains pending
and retains admission; an elapsed timeout, sequence or missing current object
cannot release it. Use the existing capacity reconciliation workflow where a
rebase is needed. This adds no control-plane SQL, placement migration or
automatic routing operation. The node snapshot does not replace PostgreSQL.

### Limits and authority assumptions

`ObjectNodeReplicationMaxEpoch` in `pkg/api/limits.go` caps epochs at 1,000,000;
existing journal, backup, file-count, buffer and transfer limits remain in force.
Preflight and promotion rehash the candidate's full data and can take substantial
time. Retained bytes and full verification I/O remain operator capacity concerns.

This protocol assumes private, exclusively owned durable directories are never
rolled back or copied into a second running writer. UUIDs/checksums do not fence
raw disk/VM clones. Do not edit roles, epochs or grants, reuse node identities,
or restore an old raw primary image. The supported backup restore path always
creates followers. This planned protocol is not a lease, quorum or election.
Unreachable-primary promotion needs independent verified fencing or a reliable
lease/quorum authority. There is no force mode, automatic failover, synchronous
replica acknowledgement, sharding or production enrollment in this milestone.
New writes still acknowledge local durability and can be lost before replication.

## Validation

Tests cover drained streaming writes and rejection of queued writes, competing
targets, stale requests/epochs, exact cursor/hash checks, corrupt baseline and
retained bytes, unfinished transfers, partitions and candidate preflight. Fault
injection and real subprocess kills occur before/after source fence and target
promotion publication; restart/retry recovers one grant and one epoch. Successor
backup/reseed, nonzero baselines, a second handoff and legacy authority upgrades
are exercised. Signed HTTP and CLI checks cover fence, catch-up, promotion,
idempotency and sealed export after restart.

Existing real branded-gateway lost PUT and multipart completion tests also run
through a handoff behind an unchanged backend URL. Recovery preserves overwritten
or deleted outcomes, durable rejections, multipart completion, and missing-proof
admission holds without replaying uploads. Inventory verifies logical capacity
on the promoted node while preserving unsettled holds.
