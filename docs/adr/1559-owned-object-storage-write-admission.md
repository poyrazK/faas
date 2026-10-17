# ADR-1559: Admit new object writes within retention and disk headroom

- Status: Accepted for development qualification; no production enrollment.
- Date: 2026-10-17
- Related: [ADR-1543](1543-owned-object-storage-write-receipts.md), [ADR-1550](1550-owned-object-storage-replica-durability.md), [ADR-1551](1551-owned-object-storage-journal-compaction.md), [ADR-1556](1556-owned-object-storage-automatic-compaction.md), [ADR-1558](1558-owned-object-storage-replication-profiling.md)

## Context

Bounded journal pages improved replay throughput, but the four-workflow/second
shared-host trial still accumulated pinned history. A follower outage or a
writer faster than replay can exhaust retained journal, replay or disk limits.
Compaction cannot remove required history. Protect headroom before new writes
create durable work, preserving every earlier receipt and retention pin.

The gateway conservatively retains a capacity hold after an uncertain provider
write, including HTTP 503. Repeatedly dispatching writes to a visibly overloaded
node would create avoidable holds. A node admission rejection must not become
proof about an already dispatched write.

## Decision

### Reserve before staging and publication

The daemon enables a process-local controller, configured before serving.
Library stores retain opt-in configuration. Under the catalog lock, project
retained journal/payload file count, journal bytes, replay bytes, the lag behind
the slowest registered retention or mandatory replica-confirmation pin, and
available disk after concurrent reservations. Statfs failures close admission.
Without a required pin there is no follower-lag restriction; retained history
and free-space restrictions still apply. All limits live in `pkg/api/limits.go`.

| Metric | Close new admission | Reopen a latched reason |
| --- | --- | --- |
| Projected pinned lag | At least 4,096 records | At most 2,048 records |
| Projected journal or payload count | At least 900,000 files | At most 600,000 files |
| Projected journal bytes | At least 896 MiB | At most 640 MiB |
| Projected replay bytes | At least 7 TiB | At most 5 TiB |
| Free bytes after projected reservations | Below `--min-free-bytes` (default 1 GiB) | At least that floor plus 512 MiB |

Each reason has its own latch. Healthy metrics in their middle bands do not
prevent another reason from clearing. A later admission or probe reevaluates
the latches; metrics are cached observations and do not perform filesystem I/O.
Restart clears process-local latches and counters; rebuilt durable retained
usage and current disk availability govern the next check.

Latched retained-history or disk pressure also triggers the existing bounded
compactor when history exists, even below its ordinary maintenance watermarks.
A projected large request or custom admission threshold must not leave safe,
reclaimable history idle. Maintenance keeps the same batch limits and pins;
this trigger never authorizes deletion by itself.

Reserve three records for PUT, one for a part, six for multipart completion,
and one for bucket/session creation. Completion covers freeze, receipt intent,
current publication, terminal receipt, terminal session and part-tree removal.
Reserve one potential replay payload for each PUT, part or assembly; journal
space uses the existing 2 MiB maximum record size. Disk reserves two body copies
plus journal space, covering staging and prepared replay. Estimates deliberately
include untracked writes and potential duplicate payloads. While publication is
in progress, actual retained usage may overlap its reservation conservatively.
This is a headroom policy, not a filesystem allocation guarantee: other writers,
filesystem overhead and already durable recovery work can still consume space.

New PUT and part operations reserve before reading/staging the body. New
multipart completion reserves before freezing its manifest. New bucket and
multipart-session creation reserve before their first record. Reservations last
through preparation/publication and release on all returns, including read
failure, cancellation and uncertain publication. Accepted operations are not
checked again against this policy while publishing. Existing hard retention,
body-size, disk-reservation and upload-slot limits remain authoritative.

Previously durable receipts, frozen completions and their exact replay paths
can finish under admission pressure. Their new in-flight work still contributes
to concurrent reservations. An existing receipt does not authorize a changed
request: identity, checksum, scope and historical outcome checks remain intact.
Finish earlier pending decisions before rejecting a later new operation. Do not
gate receipt recovery, reads, delete/abort/expiry, compaction, replication export,
cursor reports, confirmations or fencing on this new controller. Their existing
authority and replica-proof gates still apply. Cleanup can create journal
records, so this policy does not promise an absolute bound against an unlimited
stream of metadata cleanup operations.

Pressure returns retryable S3 `503 SlowDown` and `Retry-After: 1`. It creates no
new receipt or frozen manifest. Absence is still not a negative receipt.

### Check visible pressure before a new gateway hold

For an owned-node backend, the S3 adapter supports a private signed HEAD Bucket
probe with `X-Gregale-Write-Admission` (operation) and `X-Gregale-Write-Size`
(canonical decimal bytes). Require exactly one signed selector and size, a
valid operation, and an unconditional, body-free bucket HEAD. Reject selector
combinations, object paths, queries, ranges and ambiguous values. The probe
checks primary authority, bucket existence and projected pressure. It performs
no durable mutation, allocates no reservation and promises no write outcome.

The probe returns `X-Gregale-Write-Admission-Status: ready` or authenticated
`503` with `blocked`, and `Cache-Control: no-store`. It bypasses the replica
response gate because it carries no object or receipt proof. It cannot advance
received/applied/confirmed cursors or release a capacity hold.

The branded gateway checks before staging a new credential PUT and before new
multipart initiation, part transfer or completion admission. Already prepared
PUT URLs and frozen central completion resumes retain their prior paths. A
blocked probe returns SlowDown before a new hold/transfer/manifest is created.
The read-only probe uses one attempt. Unavailable or ambiguous probes fail
closed; a successful older node's HEAD without this header means no hint during
a rolling upgrade. Ordinary R2/S3 providers perform no extra RPC.

The actual node gate runs again. Pressure can rise between probe and dispatch;
a subsequent 503, timeout or lost response remains uncertain and retains the
existing gateway hold. Never classify HTTP 5xx as definitive rejection, replay
an unknown PUT automatically, or treat a ready probe as reserved capacity.
Other direct/prepared upload paths retain conservative missing-proof holds;
this change does not add a durable distributed reservation protocol.

### Operate without changing durable formats

Expose startup flags for enablement and high/low thresholds, plus a recovery
free-space watermark derived from the existing upload floor. Validate before
opening the data directory; reject invalid hysteresis or limits above hard
retention caps. Disabling the controller leaves existing hard gates in force.
Do not retire followers, downgrade replica durability, prune required history or
change authority automatically to relieve pressure.

Publish cached bounded metrics for enablement, block state, individual reasons,
active admissions, accepted/rejected operation counts and reserved resources.
Probe responses remain visible through normal request metrics. Configuration
and reservations are not serialized. Storage format 4, qualification report 3,
SSH agent protocol 2 and SQL schemas remain unchanged.

## Validation

Exercise lag and byte pressure with registered followers, bounded safe
compaction, hysteresis and reopening. Cover concurrent in-flight reservations,
disk probe failures, cancellation, failed staging, rejected bodies that are
never read, and recovery of older durable intents while new writes are refused.
Verify no receipt/manifest is invented, exact receipt replay and frozen
completion still finish, and cleanup/read/export/report paths retain their
previous contracts.

Test signed protocol bounds and ordinary/older provider compatibility. Use the
real gateway and owned S3 adapter to verify visible pressure creates no new
capacity hold, while post-probe rejection and lost committed responses retain
their uncertain holds. A ready probe must never confirm a replica. Run existing
durability, process-death, multipart, compaction, gateway recovery and built
process qualification checks. Record development overload/recovery evidence
without claiming independent-host capacity.

### Development overload evidence

The 2026-10-17 final built-process local-mode drill used ordinary compaction
watermarks and three bursts of 64 attempted 64 KiB writes with a paused
registered follower. At a 32-record projected lag
high and 8-record low it accepted 30 tracked PUTs and throttled 162, stopping
each burst at 30 durable records. At a 1 MiB replay high and 512 KiB low it
accepted 45 untracked overwrites and throttled 147, retaining 960 KiB before
each pause. Catch-up/reopen completed within 0.395 and 1.121 seconds respectively.
No rejected operation published a record or invented a receipt. Every admitted
current object remained readable and survived restart. These are deliberately
small development thresholds on a shared host/filesystem, not production
capacity measurements.

Final process qualification passed all 12 standard checks in each durability mode. The
earlier 60-second-per-mode soak also passed all checks and scheduled interruptions;
it ran before the additional pressure-driven maintenance trigger and alongside
other Go validation on the same host.
