# ADR-1567: Bounded, resumable source inventory audits

Status: Accepted

## Context

ADR-1566 makes ordinary logical usage observations constant work, but its
background health evidence still comes from a complete source scan under the
catalog read lock. The scan can delay publication for up to its 45-second
deadline and repeatedly time out as a catalog grows.

## Decision

Keep explicit source inventory, backup verification and fenced capacity recovery
unchanged. Replace only the daemon's periodic inventory worker with a resumable
state machine. Each step has record and soft elapsed-time budgets, including
directory boundaries and projection membership rows. Release the catalog lock
between steps. Directory enumeration uses bounded Linux directory-entry batches
and cookies, never a sorted in-memory listing of an entire directory. Index
membership checks use seek cursors and bounded point reads.

Checkpoint the phase, source revision, directory cookies and buffered names,
partial totals and diagnostics in a sealed private record in `.catalog-index.db`.
It remains derived state, omitted from backups and replication. A failed or
canceled step cannot publish new health. Checkpoint writes precede publication
of complete results. Metrics read only cached progress.

A cycle covers exactly one inventory boot/revision and applied journal position.
Any source publication or
uncertain source decision invalidates that revision and restarts coverage before
another record is accepted. Directory cookies are also bound to directory
identity. Reopening a node rebuilds the catalog projection and discards old audit
checkpoints; startup recovery can change source files, so old coverage is never
reused across recovery, restore, copied directories or promotion. Interrupted
workers in the same boot resume their committed checkpoint.

This is deliberately conservative. Writes make progress between bounded turns,
but sustained catalog mutation can prevent a cycle from finishing. Expose restart
counts and coverage age; do not refresh freshness merely because work continues.
A completed healthy cycle credits its start time, the oldest coverage time,
rather than extending old verification to its completion time. A cycle taking
longer than the existing 900-second freshness limit therefore cannot establish
fresh counters. Improving completion under sustained writes would require
mutation-aware coverage and is separate work.

Source metadata, selected blob types/lengths, terminal completion proofs, logical
totals, per-bucket and per-upload projection membership and physical-file
diagnostics retain their existing checks. Projection drift latches the index
unavailable. Source damage immediately withdraws counter health when observed;
only a complete healthy cycle or complete explicit audit can restore it.
Neither partial totals nor a damaged reference can become a zero observation or
release an uncertain-write reservation. Physical byte totals remain samples.

Defaults are 64 work units or 25 ms per step, with maxima of 256 units or 100 ms.
Active cycles resume after 10 ms; completed cycles wait the existing minute.
All bounds live in `pkg/api/limits.go`. Durable metadata I/O and a single bounded
record validation can exceed the soft time budget; there is no hard latency SLA.

## Consequences

Background work and retained memory are bounded per turn; total cycle work still
scales with source size. Checkpoints add local metadata writes. Startup recovery,
checksum-worklist construction, explicit usage requests, source inventory and
fenced recovery still have their documented scan/lock costs. This change does
not establish production enrollment, independent-host qualification or a
completion guarantee under continuous mutations.
