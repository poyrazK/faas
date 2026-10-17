# ADR-1550: Replica-confirmed owned-node acknowledgements

- Status: Accepted for the local prototype; no production enrollment.
- Date: 2026-10-16
- Related: [ADR-1543](1543-owned-object-storage-write-receipts.md), [ADR-1544](1544-owned-object-storage-multipart.md), [ADR-1548](1548-owned-object-storage-replication-journal.md), [ADR-1549](1549-owned-object-storage-primary-switchover.md)

## Context

The existing primary acknowledges local journal/catalog durability while a
follower catches up asynchronously. Planned switchover transfers authority safely
but does not strengthen those write acknowledgements. An optional replica mode
must withhold success until the designated follower has durably applied the
operation. Receipt recovery and inventory must enforce the same boundary;
otherwise a local terminal receipt could settle a gateway admission before its
replica was durable. Timeouts cannot roll back an already committed local write.

## Decision

Keep local durability as the default. Add offline operator command
`replication-durability --data-dir PRIMARY --mode replica --replica-id UUID`.
The initialized, replicated primary records the policy in its sealed replication
state under the existing process lock. State version 3 includes mode, policy
UUID, designated follower UUID, confirmed sequence/hash and confirmation time.
Earlier state remains readable; older binaries reject version 3. Upgrading or
restarting a node never silently changes its configured durability mode.

Confirmation sequence -1 means no confirmation. Mode changes retain the same
journal stream, sequence and authority epoch and create no data mutations.
Repeating the same mode/follower configuration preserves its policy identity.
Disabling replica mode is refused while the current applied tail is unconfirmed.
This prevents an offline configuration change from weakening unresolved writes.

### Authenticated durable confirmations

The catch-up worker reports only after a successful, fsynced applied checkpoint,
including a partial/bounded step's last applied prefix. Received records or
partial blob bytes cannot advance the reported position. A failed persistence
step sends no report. Reports also repeat when a complete catch-up step has no
new work, allowing lost acknowledgements to recover without reapplying data.

Use private SigV4 POST `/?gregale-replication=confirm` with a canonical JSON body
binding version, stream UUID, primary UUID, epoch, policy UUID, follower UUID,
applied sequence and hash. Require an authenticated SHA256 body; reject unsigned
payloads, unknown/duplicate fields, selectors and unsafe framing. The existing
operator credential is a trusted node-wide administration identity; tenants
cannot call this protocol. Cross-host traffic retains the encrypted tunnel or
private TLS requirement. Confirmation does not provide leader election.

The primary verifies the exact current identity/policy/follower, refuses
regression or positions beyond its applied head, and verifies the hash against
the retained journal or snapshot baseline. Persist and fsync the updated
confirmation before acknowledging the report or waking response waiters. An
idempotent retry rereads and fsyncs durable state to resolve publication whose
acknowledgement was lost. Never expose a speculative in-memory confirmed point
after a failed checkpoint write. Confirmations may still reach a fenced primary
for its sealed stream during planned handoff.

### Service response and recovery boundary

After a local operation finishes, the private S3 handler captures the current
applied position and withholds successful response headers until the current
policy confirms that prefix. No lifecycle or catalog lock is held while waiting:
replication export, reports, fencing and shutdown can progress. Concurrent local
operations may publish before capture; this first implementation conservatively
waits for the whole captured prefix instead of maintaining per-operation indexes.
An unrelated unconfirmed write can therefore delay an older receipt or read.

The gate covers successful S3 operations and positive private receipt/inventory
responses. Conditional 412 rejection also waits because its durable negative
receipt is settlement evidence. Other validation/authentication errors do not
establish a committed write. Replication RPCs bypass the gate. Local maintenance
such as checksum worklists is not replicated data or a customer write outcome.

On timeout/cancellation return 503 `ReplicaDurabilityPending`, clear prepared
object length, ETag, checksum, range and receipt headers, and stop the original
response body. Do not undo catalog decisions or delete historical receipts.
The existing gateway/provider classification keeps dispatched PUT and multipart
admission uncertain. Private receipt probes remain unavailable until confirmed;
missing proof continues to mean uncertainty, never rejection. Coherent inventory
cannot refund capacity based on an unconfirmed local view. Existing database
ownership, usage/billing counters and conservative reservation behavior remain.

The disk engine's `Store` methods retain local publication semantics. The S3
service enforces replica responses; other in-process consumers must explicitly
call `WaitForReplica` after publication. Operator backup/configuration/fencing
commands are local administrative operations, not customer write acknowledgements.
The response wait ends at the captured point; later writes need their own proof.

### Replacement, backup and authority changes

Operators can replace the designated follower while retaining replica mode.
Take a verified current primary snapshot, bootstrap a new follower, stop the
primary and configure that new UUID. Generate a new policy UUID and reset its
confirmation. Old reports are refused and no response is released until the
replacement confirms the relevant prefix. This can recover a failed/mistyped
follower binding without weakening durability or forcing local-mode settlement.
No configuration operation permits a local downgrade of an unconfirmed tail.

Backups retain the primary's policy for verification; restored format-4 nodes
remain fresh, fenced followers and clear copied confirmation authority. A newly
selected follower can confirm a verified snapshot baseline without replaying
pre-baseline entries. Baseline/catalog hashes and existing backup limits apply.

A replica-mode primary can issue a planned handoff only to its designated
follower. Promotion still requires exact final equality and complete verification.
The new primary keeps replica mode with no follower bound; the verified handoff
certifies its old prefix, while new S3 mutations are refused until another
follower is configured. New recovery/expiry records can extend the unconfirmed
tail and require that new follower. Preserve the old fenced directory until
the replacement copy has been verified. No automatic local fallback or new
election/quorum/force-promotion protocol is introduced.

### Bounds and observations

New limits live in `pkg/api/limits.go`: confirmation bodies at most 4 KiB and
response waits at most 10 seconds, also bounded by the request context. Existing
32-record/64-MiB catch-up steps, transfer timeout,
journal, backup and free-space limits remain. Large writes or accumulated lag
can exceed the response wait even with a healthy follower; retain the original
receipt and settle later. Local mode remains available through explicit, safe
configuration, with its existing weaker acknowledgement semantics.
Continuous follow scheduling is updated by
[ADR-1553](1553-owned-object-storage-replication-waits.md).

Memory-only Prometheus samples expose required/bound policy, whether a
confirmation is known, lag in records and whether the sampled applied head is
confirmed. A waiting-response gauge and wait histogram use bounded outcome
labels. No bucket, receipt, stream or node identity becomes a metric label.
These observations are not leases or proof of write authority.

## Validation and limits of the guarantee

Tests exercise applied versus received prefixes, final PUT/multipart records,
positive and negative receipt gating, deletion and inventory responses, mode
change/replacement safeguards, epoch/identity/hash refusal, source/follower
restarts, checkpoint errors and lost confirmation responses. Real processes are
killed after follower applied and primary confirmation checkpoints. Real branded
gateway recovery keeps committed/rejected/missing PUT outcomes and capacity
reservations pending before confirmation and settles exact proofs afterward
without replaying uploads. CLI and metrics paths are exercised.

Replica-mode success proves the primary and designated follower completed their
local filesystem durability boundaries for the captured prefix. Qualify separate
hosts/disks with working fsync. This is not a quorum or permanent protection from
loss/corruption of both copies. An unacknowledged local mutation can still exist
and require recovery. Unreachable-primary failover continues to need independent
fencing or reliable lease/quorum authority. Retention/compaction, repair,
automatic failover and production enrollment remain later work.
