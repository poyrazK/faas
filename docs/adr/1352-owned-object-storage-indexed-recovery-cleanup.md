# ADR-1352: Indexed online recovery cleanup on the owned object node

Status: Accepted

## Context

ADR-1350 makes acknowledged historical receipts and terminal multipart sessions
collectible after 30 days. ADR-1351 durably delivers acknowledgements. The offline
collector still scans both catalog directories for every maintenance page and
holds the catalog lock for the page. It cannot provide bounded online work as
the receipt catalog grows.

## Decision

Maintain a local bbolt B+tree in `.recovery-index.db`, mode 0600, under the node's
exclusive process lock. Index only canonical UUIDv7 identities with exact sealed
terminal records and persisted settlement acknowledgements. Completion/rejection
requires both matching multipart proofs and both acknowledgements; abort/expiry
also waits until session expiry. Eligibility is the latest applicable issue,
acknowledgement or expiry time plus 30 days, rounded upward to a millisecond.
Legacy UUIDv4 history and uncertain or active operations never enter this index.

The index maps identity to eligibility and next check time, with a second B+tree
ordered by next check time. Its transactionally persisted count and checkpoint
cover the source journal stream, sequence and hash. Protected identities are
deferred for one hour; unchanged source eligibility preserves that deferral.
The checkpoint is a local projection position, never a write outcome or fencing
authority. Irrelevant journal entries advance an in-memory position, persisted
on the next index mutation or clean close. An index update failure pauses
cleanup without failing an already durable source write.

Startup first completes normal authoritative catalog recovery. A matching local
checkpoint reuses its index. A lagging checkpoint with a verified journal anchor
replays at most 4096 entries, deriving eligibility from the final sealed source
records. Missing, interrupted, foreign, ahead, pruned or older checkpoints rebuild
from the catalog at startup in transactions of at most 128 candidates. An
in-progress marker forces another rebuild after an interrupted initial build.
Startup rebuilding and ordinary startup recovery can scan the catalog; background
cleanup never does. Invalid index files or index I/O errors pause collection and
expose unhealthy status while valid source data remains serviceable. Symlinks and
hard-link aliases are refused. Operators can rebuild by stopping the daemon,
removing only this derived file, then reopening with the current executable.

The daemon runs cleanup by default only on already opted-in format-5 primaries;
it never implicitly enables retention. Each one-second tick examines at most
eight due identities, configurable up to 128. A soft 25 ms work budget,
configurable up to 100 ms, and cancellation are checked between identities.
Each identity holds the catalog mutex for its own decision and releases it
before the next one. A started retirement must finish its durable fsync sequence;
the time budget is not a hard latency guarantee. Failures retry after 30 seconds.
Followers and fenced nodes cannot run this primary cleanup worker.
Cleanup also defers while a source write or journal application is pending;
ordinary recovery completes that work before another cleanup decision. The
cleanup worker never takes on pending payload replay within its work budget.

Every selected identity is revalidated against its current sealed receipt,
settlement time, matching terminal session and live object pointer under the
catalog mutex. A stale hint cannot authorize collection. A missing receipt and
session only remove a derived hint and never settle central unknown writes.
Terminal multipart directories must contain only `session.json` and an optional
empty `parts` directory; bounded directory reads prevent recursive part removal
from becoming unbounded online work. Nonempty parts defer paired retirement.

Each authorized retirement uses the existing single journaled recovery decision.
The monotone first-use barrier is fsynced before paired receipt/session removal,
with the existing crash replay semantics. Idle or protected ticks do not journal
a new barrier. No node format or replication message changes are needed.
Backups exclude the local index but retain all sealed source proofs and barriers.
Restore verification stays read-only. Restored nodes remain non-serving followers;
an explicitly promoted primary rebuilds eligibility from its replicated catalog.

Expose cached aggregate `faas_object_node_recovery_cleanup_*` metrics for enabled,
ready and deferred state, indexed candidates, next-check due age, last successful
step, examined/protected/stale identities, removals, index rebuilds/replay, steps
and failures. Counters are process-local. Candidate counts include current object
proofs; due age is scheduling delay, not proof that an identity is collectible.
Scrapes perform no index or catalog I/O and contain no object identity labels.

## Consequences

Online cleanup work scales with selected identities rather than catalog size.
The B+tree adds local write and fsync cost to acknowledgement and retirement;
startup rebuilding remains proportional to catalog size. bbolt reuses freed pages
but does not promise to shrink its high-water file size. Journal compaction and
registered follower pins continue to govern retained replay history separately.

The offline collector remains available. The existing 30-day compatible central
and node restore contract is unchanged. Current proofs, legacy identities,
undelivered acknowledgements and central journals still require capacity.
Bucket listing and active multipart expiry retain their existing scan behavior.
This milestone does not qualify independent hosts, power-loss durability or
dedicated data disks; those still require the separate-host qualification run.
