# ADR-1350: Settled recovery proof collection on the owned object node

- Status: Accepted for opt-in development qualification.
- Date: 2026-10-17
- Related: [ADR-1332](1332-owned-object-storage-write-receipts.md), [ADR-1333](1333-owned-object-storage-multipart.md), [ADR-1336](1336-owned-object-storage-backups.md), [ADR-1340](1340-owned-object-storage-journal-compaction.md)

## Context

Journal compaction preserves historical write receipts and multipart tombstones.
Those files accumulate even when object data is overwritten or deleted. Deleting
them by age would let a delayed request reuse its identity and could remove the
only evidence needed by an uncertain gateway write. UUIDv4 identities have no
bounded first-use window, so they cannot be safely forgotten without another
permanent record or a change to the retry contract.

## Decision

New gateway write and multipart identities use canonical UUIDv7. Their first use
must fall within 30 days of the encoded issue time and at most five minutes into
the node's future. Exact existing records are resolved before this check, so an
unsettled operation can still recover after that window. UUIDv4 and other legacy
identities retain their existing indefinite proof and replay behavior.

Add an explicit, irreversible format-5 opt-in on a journal-enabled primary.
Local durability remains supported without a follower. The primary journals a
sealed `recovery.json` containing the 30-day contract and a monotone issue-time
cutoff. Followers apply this state in order and upgrade their format marker
before accepting it. Older executables reject format 5 and the new journal
metadata path. Enabling replication again cannot downgrade the format.

The authenticated private settlement RPC accepts an exact receipt identity,
physical bucket, key, kind, terminal status, size and committed ETag. Multipart
completion additionally binds the frozen manifest hash. Abort settlement binds
an aborted or expired native session with no completion receipt. The gateway
sends this acknowledgement only after its owner commits terminal settlement.
The node persists its own acknowledgement time; retries retain that time. These
acknowledgements are recovery metadata and never release gateway capacity.

Gateway delivery is separately enabled with `owned_node_settlements`; managed
providers and older owned-node configurations receive no new RPC. Delivery is a
single bounded attempt after settlement. A lost acknowledgement retains proof;
there is no durable delivery outbox in this milestone. An operator can retry an
exact already committed settlement through the signed adapter or offline CLI.
Enabling delivery does not change the physical backend fingerprint.

Collection is an explicit, cancellable offline maintenance page, with bounded
working memory and at most 128 identities by default (maximum 1024). Its scan
can read the complete directories; it is not an online latency guarantee.
An eligible record must have a UUIDv7 identity older than the cutoff and an
acknowledgement at least 30 days old. Current-object proofs, legacy identities,
unacknowledged outcomes, active uploads and unfinished decisions remain pinned.
An aborted/expired session must also be beyond its original expiry window.

One journal mutation records each retirement of a receipt and its associated
multipart session together. Application fsyncs the issue-time barrier before
unlinking either record, checks the expected seals and absence of a live object,
then fsyncs the affected directories. A crash resumes the same ordered journal
decision before primary receipt or multipart recovery. The barrier survives
restart, promotion, journal compaction and format-5 backup/restore; its metadata
keeps only the last retirement decision. Journal history continues to obey
registered follower pins and replica confirmation. Collection grants no new
authority to prune replication history or to promote a follower.
Missing or corrupt barrier metadata is fatal on a format-5 serving node. The
initial upgrade is journaled before its format marker changes, so an interrupted
first publication can replay before strict barrier validation.

## Recovery contract and consequences

Missing proof remains unknown, including after collection. An expired fresh
write returns a conflict; it is never classified as definite rejection by the
owned gateway. Native multipart IDs cannot be recreated from a retired session
identity. Collection never retries an unknown PUT or changes current bytes.

Node backups include the barrier, acknowledgements and all still-required
proofs. They remain verifiable and restore as non-serving followers. Operators
must retain compatible gateway/PostgreSQL recovery points within the 30-day
post-acknowledgement window, or restore a matching older node backup containing
the proof. Restoring older gateway state against a newer collected node may
leave old pending admissions unresolved; absence cannot release them. Arbitrary
independent rollback of gateway state is not a supported recovery guarantee.
Use current executables for restore and keep node clocks accurate. Restoring an
older backup does not extend the first-use window of absent UUIDv7 identities.

No old proof is migrated or deleted just by enabling the feature. Current-object
proof storage remains proportional to current tracked objects; legacy and
undelivered acknowledgements still require space. Automatic gateway delivery
retry, online directory indexing and automatic metadata collection remain later
work. Production qualification on independent hosts and disks is still pending.

## Validation

Validation must cover exact and conflicting acknowledgements, missing evidence,
both terminal outcomes, live and legacy pins, stale request resurrection,
multipart abort/completion cleanup, interrupted paired deletion, restart,
replication, compaction, backup/restore and promotion. Gateway tests must show
that acknowledgement follows database settlement and that delivery failure
does not change the customer outcome or capacity accounting.
