# ADR-1345: Bounded automatic journal compaction

- Status: Accepted for development qualification; no production enrollment.
- Date: 2026-10-17
- Related: [ADR-1340](1340-owned-object-storage-journal-compaction.md), [ADR-1342](1342-owned-object-storage-replication-waits.md), [ADR-1343](1343-owned-object-storage-publication-preparation.md), [ADR-1344](1344-owned-object-storage-ssh-qualification.md)

## Context

Offline compaction reclaims journal history safely, but requires stopping node
processes. A continuously used primary or follower can reach a retention limit
between operator maintenance windows. Running the offline implementation under
the live catalog lock would walk and hash an entire filesystem and compete with
foreground traffic. Upload preparation also depends on retained files outside
that lock, so a reference-free payload is not necessarily safe to unlink while
an upload is preparing it.

## Decision

Add `Store.CompactStep` and a single tick-driven `RunCompactor`, retaining the
existing format-4 sealed floor/checkpoint contract. Enable maintenance by default
in `object-storaged` and the continuous `replica-catch-up --follow` worker. Each
owns its existing process lock; no second Store, HTTP mutation route, SQL change,
force pruning or new write authority is introduced. One-shot catch-up does not
start a background worker.

### Triggers and resource bounds

Start a cycle at any of 750,000 retained journal files or replay files, 768 MiB of
journal metadata, 6 TiB of replay payloads, less than 4 GiB of available space, or
less than 25 percent of filesystem blocks available to the process. Ignore space
pressure when no retained files exist. Keep the existing hard budgets of one
million files/records, 1 GiB of journal metadata and 8 TiB of replay bytes.

Run one step per 100 ms, with defaults of 128 journal records or 128 replay
directory entries per step, and maxima of 1,024 for either. Cap journal metadata
read per floor at 1 MiB; one indivisible record may exceed this, up to the existing
2 MiB record ceiling. Payload sizes do not add hashing work to a cleanup step.
Use an open directory cursor and bounded pages instead of sorting a directory
or creating an unbounded deletion list. Back off five seconds after errors or a
complete sweep blocked by follower pins. Configuration and limits live in
`pkg/api/limits.go`.

Idle ticks use cached usage and a filesystem space query, without reading or
fsyncing replication metadata. A pinned retry waits on cached positions until
its backoff expires or a cursor advances. Actual cleanup first reconciles durable
state so an uncertain registration or confirmation save cannot be overwritten.

These are work bounds, not a latency SLO or OS I/O isolation. Metadata reads,
unlinks and fsyncs still serialize under the catalog lock; a slow filesystem can
stall one step. Sustained publication can exceed configured cleanup throughput,
and no safe prefix may exist. Preserve capacity rejection and retention pins in
those cases; never retire a follower or weaken replica durability automatically.

### Reference index and pending outcomes

Build in-memory digest reference counts while validating the retained journal
on startup. Count each newly published journal decision once, including a record
whose fsync acknowledgement was lost. Track the highest indexed sequence to
avoid counting adoption/retry twice; a gap or inconsistent count stops cleanup.
The map is bounded by retained digest identities, not a second copy of journal
metadata or object payloads.

Load active committing receipt pins from vetted receipt metadata under the
process lock, including intents below the current floor. This adds an initial
receipt metadata pass on follower startup. Maintain pins when receipt metadata
is durably applied; terminal proofs release their replay pin, not their receipt
file. A primary's pending in-process receipt is an additional conservative pin.
The follower can pause between intent, current publication and terminal proof,
so its receipt pins cannot depend on primary-only recovery state.

Indexes are never persisted or used as write outcome, authority or restore
proof. Restart rebuilds them from the validated disk. Historical terminal
receipts, multipart sessions/manifests/tombstones, current catalog data and
customer capacity state are never garbage-collected by this worker.

### Bounded floor publication and deletion

A cycle captures a finite applied target. Each record step selects a bounded
prefix no later than the minimum of that target, local applied position, every
registered follower cursor and the designated replica's confirmed position.
Unknown replica confirmation pins the current floor. Followers require
received == applied and adopt any already published decision before selecting
work; partial transfers remain untouched.

Read and validate the selected journal prefix from its previous floor hash,
checking seals, authority, exact names, regular single-link files and file
fingerprints. Publish/fsync the new floor with pruning pending before unlinking
any selected record. Preserve the latest applied head, registrations, policy and
confirmation fields if writes/reports have progressed between steps. Fsync the
log directory before decrementing reference counts or cached usage. Fsync the
retention directories before sealing the completed marker. Use the same marker
as offline maintenance, so existing startup recovery understands an interrupted
step.

After advancing as far as currently possible, sweep replay files in bounded
pages. Delete only a digest with zero retained journal references and zero
committing receipt pins. Borrow the existing upload preparation slot without
waiting while holding the catalog lock; defer payload cleanup if a preparation
owns it. This prevents deleting a retained file between its preparation digest
check and publication. Existing open export descriptors can finish reading an
unlinked immutable inode; unregistered followers retain no history guarantee.

Directory-sync uncertainty keeps unlink charges and reference decrements pending
in memory until a successful retry. A new write may reinstall the same digest;
its new inode is charged separately and remains protected by its own journal
reference. An uncertain floor/completion save marks replication state dirty;
normal durable reconciliation or restart must resolve it before serving a stale
position. A closed/fenced store starts no new maintenance. Close drains a step
and closes its directory cursor; startup can finish a checkpoint sealed before
fencing.

Startup retains the existing full retained-chain/tree validation and idempotent
cleanup before Open returns. Recovery work is not limited to one online tick.
Offline compaction retains its full catalog/payload integrity preflight.

### Integrity scope

Online maintenance relies on durable publication, validated journal metadata,
separate immutable live/replay files and exclusive private filesystem ownership.
It does not hash all current object bytes or replay payloads before each floor;
doing that under continuous traffic would defeat the bounded maintenance goal.
It never removes live data paths. Background checksum scrubbing, offline full
verification and verified backups remain the byte-integrity mechanisms. This
differs from the explicit offline compactor's whole-tree preflight and is not a
repair or independent-copy durability guarantee.

### Live replication and change waits

A source floor can advance between a follower's Head and Entry calls. On an
explicit below-floor response during position validation, reread Head and accept
only an exact floor sequence/hash under the same stream, owner and epoch with a
monotonic head. A position below that floor still requires reseeding; a different
hash or authority fails as a fork. Never skip unapplied history.

Change-wait revisions continue to include BaseSequence/BaseHash, authority,
applied data and registration generations. Exclude checkpoint cleanup flags and
timestamps, which do not grant new replay or confirmation evidence. Upgrade
continuous workers with the daemon so they understand floor movement. The SSH
qualification agent protocol advances to version 2 for maintenance settings;
preinstall matching current binaries on all qualification participants.

### Controls and observations

Both daemon and continuous worker expose `--compact`, `--compact-records`,
`--compact-journal-bytes`, `--compact-replay-bytes`, `--compact-min-free-bytes`,
`--compact-step-records` and `--compact-step-blobs`. Disable with
`--compact=false`. Existing operator-triggered offline compaction remains
available, including maintenance when a hard ceiling interrupts receipt recovery.

Export cached aggregate `faas_object_node_retention_compaction_*` counters for
steps, failures, cycles, records, replay files and bytes unlinked; gauges for
active, pinned, deferred and last successful step. Bytes unlinked are namespace
accounting, not measured freed blocks; open readers and unrelated files still
consume space. Scrapes perform no custom disk walk and add no object/node labels.
Continuous follower JSON includes its cached compaction status.

Qualification records enabled/disabled maintenance and the configured file
watermark, forwards settings through SSH and role reversal, and samples primary
compaction counter peaks. Peaks can miss activity or reset across child restarts;
they are not lifetime sums. A low test watermark forces automatic work in short
mixed workloads without changing production defaults.

## Validation

Require bounded-file and metadata-budget checks, shared payload and registered
cursor pins, unknown/partial replica confirmation, primary/follower committing
receipts below the floor and after restart, upload preparation exclusion,
concurrent writes/reads and historical outcomes, lost checkpoint acknowledgements,
conservative deletion accounting and reuse after an uncertain unlink. Test exact
floor races, genuine reseeding, changed authority, cancellation and unsafe aliases.

SIGKILL child processes before/after checkpoint publication, after record/blob
unlink, after directory sync and after completion. Require acknowledged bytes and
historical proofs after recovery, eventual cleanup and verified backups. Retain
offline compaction, multipart, receipt, authority and follower regressions, and
qualify matching mixed gateway workloads with automatic work enabled/disabled in
both durability modes. Independent-host capacity qualification remains pending
configured disposable hosts; this change makes no production capacity claim.

On 2026-10-17, two matched enabled/disabled pairs used identical binaries, a
16-file watermark, four workers, two cycles of four workflows, 64 KiB payloads
and seed 1. Both durability modes passed all 14 checks in every run, with zero
unexpected request failures. Enabled runs sampled 43–44 primary records and
10–11 replay files pruned per process; disabled runs sampled zero. Registered
followers continued through floor movement and recovery drills.

These short shared-host samples show reclamation and correctness, with mixed
latency results. Across the four per-cycle samples, replica-mode GET p95 was
90–224 ms enabled and 84–134 ms disabled; local-mode PUT p95 was 145–244 ms enabled
and 164–203 ms disabled. Each operation has only four workflow samples per cycle,
so p95 is the maximum observation. Cleanup bounds are work bounds; independent
hardware and longer runs are still needed to size its foreground latency impact.

A separate OpenSSH run with matching protocol-2 binaries and the same low
watermark passed all 14 checks per mode, including promotion and follower/primary
restart. Sampled primary peaks showed 43–48 records and 10–11 replay files pruned.
Both participants explicitly shared a kernel/filesystem; host separation and
physical failure domains remain unverified.
