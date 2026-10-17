# ADR-1354: Indexed object listings and bounded multipart expiry

Status: Accepted

## Context

Object listing retains a bounded page but reads every object catalog record.
Multipart initiation and bucket deletion also scan every retained session to
count active uploads. Expiry limits newly expired sessions but continues the
full directory walk and can recursively reclaim 10,000 parts under one catalog
lock. These costs grow with retained history, even when the requested page is
small or no upload is due.

## Decision

Every serving primary maintains a private bbolt projection in
`.catalog-index.db`, mode 0600, under its existing exclusive process lock. This
works with formats 3, 4 and 5 without enabling recovery retention or changing
the source format or replication protocol. The index contains generation-scoped
ordered object keys and blob identities, a reverse physical-path mapping,
active-upload counts, and upload scheduling hints ordered by due time.

Primary startup first finishes authoritative source recovery, then discards and
rebuilds this derived file from the recovered catalog. Object and upload build
transactions contain at most 128 records. No completeness checkpoint is trusted:
format 3 has no source journal, and a process may die between source publication
and projection publication. Missing, truncated, foreign and stale regular index
files are therefore rebuilt in the same way. Symlinks, hard-link aliases,
nonregular files and nonprivate permissions are refused. An index creation or
update failure disables indexed operations and exposes unhealthy cached status;
it never changes the result of an already durable source mutation. A restart
rebuilds from source. Ordinary reads and writes remain governed by their existing
source recovery and fencing rules.

Source publication and index updates serialize under the same catalog lock.
Journal apply observes successful authoritative changes; format-3 publication
and deletion use equivalent local hooks. The file is excluded from backup
manifests and replication. Backup verification remains read-only, followers do
not build it, and a serving primary opened after promotion rebuilds it. Backup
and promotion retain strict source-path allowlists and reject aliased files.

Listing seeks to the greater of prefix and exclusive continuation boundary.
Each selected object is read from the authoritative catalog and checked against
the indexed key and immutable identity. Delimiter listings seek directly past
each returned common prefix, rather than decoding its descendants. A page reads
at most limit plus one matching source record, with at most one additional
boundary record. Byte ordering, prefix filtering, delimiter grouping and
StartAfter behavior remain unchanged. New opaque continuation tokens include
the bucket generation; a token from a deleted/recreated bucket is rejected.
Previously issued tokens without a generation remain accepted for compatibility.
Pages are coherent while their catalog read lock is held; pagination across
mutations resumes by key and does not promise a multi-page snapshot.

Active uploads and terminal sessions with remaining parts enter the expiry
queue. Every due hint is revalidated against its sealed source session, receipt,
bucket generation, expiry and assembly state before a source decision. Future
sessions are rescheduled from source; assembling sessions defer for 30 seconds
so other due sessions can progress. A vanished source only discards a scheduling
hint and does not settle an unknown central write. Current object bytes and
recovery proofs never become deletion candidates through this queue.

Each expiry step defaults to 32 candidate turns, eight part directories and a
soft 25 ms wall-time budget. Configurable maxima are 128 candidate turns, 128
parts and 100 ms. The lock is released between turns; cancellation and elapsed
time are checked between durable decisions. Pending receipts or unapplied
journal work defer maintenance instead of replaying payloads inside its budget.
A terminal status is durable before any part is removed, and unreachable part bytes are reclaimed locally in bounded turns. Once empty,
the whole parts directory uses the existing journaled removal operation. Followers
may retain additional terminal part bytes until that final entry; the durable
terminal session prevents their reuse. This requires no new replication command.
One turn removes at most one part directory, then may remove its empty parent.
Before recursive removal, a bounded directory read permits at most 16 regular
catalog/blob files and rejects nested or unexpected entries. Excessive orphan
files pause that upload until ordinary offline recovery cleans its layout.

The existing daemon expiry worker uses these bounded options through
`--multipart-expiry-sessions`, `--multipart-expiry-parts` and
`--multipart-expiry-time`, with its existing 30-second tick. Explicit abort,
completion and offline recovery retain their existing bulk cleanup behavior.
Terminal tombstones and the format-5 30-day acknowledgement contract are unchanged.

Cached `faas_object_node_catalog_index_*` metrics expose readiness, rebuilds,
failures, source records examined by listings, expiry turns and examined hints,
new expirations, removed part directories and source-work deferrals. Counters
are process-local and expose no bucket, key or receipt labels. Scraping performs
no filesystem or database reads.

Qualification readiness retries a transient 200 ms metrics sampling timeout
within its existing 15-second startup deadline. Missing metrics, cleanup
failures, fresh-proof removals and an exhausted outer deadline still fail the
qualification. Resource sampling keeps its existing short deadline.

## Consequences

Steady-state listing work depends on page size and returned prefixes; idle
expiry and active-upload counts no longer scan retained catalogs. The local
B-tree adds write/fsync and disk-space costs. It reuses freed pages but does not
promise to shrink its file. Startup remains proportional to catalog size and
must budget for authoritative recovery and a complete index rebuild. Fsync and
filesystem calls can exceed a soft step budget; these are work bounds, not hard
latency guarantees.

An index fault makes listing and new multipart admission temporarily unavailable
rather than returning an incomplete page or an incorrect count. Stop and restart
the current daemon to rebuild; do not modify source catalogs to repair an index.
If rolling back to an older executable, stop the daemon and remove only the new
derived index file before using older backup or inventory tooling.

Coherent accounting inventory, usage audits, multipart discovery, explicit bulk
cleanup and startup recovery still scan their source catalogs. This milestone
does not add sharding, distributed metadata consensus or independent-host failure
qualification. The prepared two-host qualification remains pending selected hosts
and dedicated data mounts.
