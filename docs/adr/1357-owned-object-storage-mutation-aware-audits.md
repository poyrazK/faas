# ADR-1357: Source coverage during concurrent object mutations

Status: Accepted

## Context

ADR-1356 bounds publication pauses but restarts its whole audit on every catalog
mutation. A busy node can never finish that cycle and eventually loses fresh
counter evidence. Preserving a directory cookie alone cannot prove coverage when
entries are inserted or removed behind it.

## Decision

Replace the periodic worker's single-revision scan with a source coverage ledger
and a bounded, coalescing change queue in the existing derived catalog database.
Explicit source inventory, fenced capacity recovery, backup verification and
checksum scrubbing retain their current contracts.

Build an independent source identity registry from recovered bucket, selected
object, session, receipt and active part records at primary startup. Maintain its expected
identity accumulator in process memory. Source metadata and generation are bound
into each identity. Accumulators use process-keyed SHA256 contributions, modular
256-bit addition and an independent count. The key and completeness anchor are
not loaded from a checkpoint or copied disk. Upload identities include an
aggregate of their selected parts; terminal transitions remove that aggregate in
constant work. Obsolete part rows can be reclaimed in bounded background turns.

After durable source publication, update the registry and queue in the same local
transaction as the catalog projection. This includes receipt changes, removals,
multipart expiry and paired proof/session retirement. Coalesce repeated changes
by source identity; also enqueue affected bucket generations. A projection or
tracking failure preserves an already durable source mutation and disables
indexed operations. An unexplained source revision cannot silently extend
coverage. A cold restart always rebuilds the registry and discards old coverage.

Each cycle independently discovers physical source files, visits the ordered
registry and active part identities, checks projection rows in both directions,
and reconciles queued changes. Derived registry, ledger and queue rows are authenticated with the process key.
A checked ledger records the actual source identity
and contribution observed in this cycle. Replacing or deleting a checked record
adjusts its contribution; unchanged records retain their progress. Part
observations update their parent's checked aggregate without a bulk part scan.
New identities behind a scan cursor remain reachable through the durable queue.
Independent registry traversal covers live records that directory enumeration
could skip during mutations. Extra physical records and projection rows cannot
establish matching verified coverage.

Use the existing record/time budgets for every traversal, reconciliation and
cleanup turn. Interleave reconciliation with baseline work so changing hot keys
cannot consume every baseline turn. Source records precede generation checks in
the queue. Final checks compare independently observed per-bucket and per-upload
totals with the serving projections. Publish only after baseline checks finish,
the queue is empty, the current expected and checked identities agree, and all
generation checks have completed under the publication lock. The returned
snapshot describes that final catalog state. Physical bytes remain samples.

The queue defaults to 4096 identities, accepts a configured maximum of 65536,
and has a separate 16 MiB byte bound. Bounds live in `pkg/api/limits.go`.
Overflow records a gap without rejecting a durable source write. Bounded turns
discard the old queue and coverage before establishing a new baseline. Tracking
corruption or unexplained omissions fail closed. Neither overflow, backlog work,
partial totals, cancellation nor a failed checkpoint refreshes health. Credit a
healthy cycle's start time, preserving the existing 900-second coverage limit.

Checkpoint ledger updates, consumed queue entries and scan progress atomically.
Cancellation during checkpoint commit cannot publish health or cause the next
worker to promote that canceled result. Source damage withdraws health when
observed; projection drift remains latched until rebuild. Newer failed evidence
cannot be replaced by older partial coverage. None of these observations can
settle an uncertain write or release its reservation.

## Consequences

Audits can complete during sustained traffic when validation capacity exceeds
the incoming change rate. This is a measured load envelope, not an unconditional
liveness promise. Backlog age, reconciled identities and overflows are cached
metrics. Source tracking adds local metadata I/O and disk space; record and
checkpoint I/O can exceed the soft time budget. Derived rows and bbolt allocation
can retain high-water space until bounded cleanup or startup rebuild.

No object-format, replication-command, central schema or customer API change is
needed. Backups and replication continue to omit the derived database. Native
host/disk qualification and production enrollment remain separate gates.
