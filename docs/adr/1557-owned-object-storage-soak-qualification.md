# ADR-1557: Bounded object-storage soak qualification

- Status: Accepted for development qualification; independent-host evidence pending.
- Date: 2026-10-17
- Related: [ADR-1552](1552-owned-object-storage-local-qualification.md), [ADR-1555](1555-owned-object-storage-ssh-qualification.md), [ADR-1556](1556-owned-object-storage-automatic-compaction.md)

## Context

Short mixed workloads and crash tests exercise correctness, but complete too few
cleanup cycles to characterize sustained retention pressure or request latency.
Increasing the admission duration alone does not create a soak: the existing
256-workflow ceiling can stop traffic early. Online cleanup also shares metadata
I/O and the catalog lock with customer operations, making measurements during
repeated cleanup and follower interruption necessary before sizing a deployment.

## Decision

Add an opt-in `--soak` profile to the existing disposable qualification runner.
Keep the same customer gateway, immutable byte/ETag model, historical receipt
proof checks, capacity reconciliation and post-load recovery/backup/promotion
checks. Reuse the local and SSH process arrangements. This introduces no storage
format, node API, production fault control or automatic authority transition.

### Work and disk limits

CLI soak defaults are five minutes per mode, one cycle, four workers, at most
4,096 workflows, 64 KiB bodies and a 64-record cleanup watermark. Explicit
`--duration`, `--cycles`, `--max-workflows` and `--compaction-records` values take
precedence. Ordinary qualification keeps its previous profile defaults and
256-workflow ceiling. Go callers can select `DefaultSoakOptions` explicitly.

`--soak-workflows-per-second` optionally paces aggregate workflow starts at an
integer target from 1 to 32 per second, with a one-workflow burst. Zero (default)
leaves starts unpaced. Actual throughput may be lower under contention or replica
gating. Pacing checks the admission deadline and keeps its final idle gap within
the requested observation window; it never admits work after that deadline.

Allow at most 16,384 configured workflows across all cycles of one soak mode,
with the existing 16-worker, 16-cycle, 8 MiB body and 30-minute aggregate admission
bounds. A workflow has a fixed operation sequence, giving bounded request counts,
exact latency sample storage and expected-receipt/model memory. Keep the existing
30-second deadline per admitted workflow and drain in-flight work before checking
the model. The duration bounds admission, not verification, backup or recovery.
Do not replay customer PUTs after unknown outcomes.

Use the existing worker-specific object keys for PUT, overwrite, multipart and
delete operations. Receipts, multipart sessions/tombstones, backups and journal
history still consume space; a fixed live key set does not bound all metadata.
Expose `--disk-budget-bytes`, positive and at most the existing 2 GiB disposable
file budget per mode. Sampling includes local fixture files and remote files.
This is an admission stop based on sampled file sizes, not an OS quota or a bound
on allocated blocks. Already admitted operations and maintenance/backup copies
can overshoot it. Existing write free-space floors remain in effect.

Soak admission requires a successful disk observation no older than five seconds.
Wait for an initial observation before admitting work; stop on a failed/stale
probe or budget exhaustion. Failed measurements must not become zero usage.
No test fills a live disk or accepts an existing storage directory.

### Interval evidence

Every five seconds by default, write one JSONL window in the private mode root;
`--soak-interval` ranges from one second to one minute. Create a separate exclusive
0600 `soak-<cycle>.jsonl` file per cycle, limited to 16 MiB with a 32 KiB row cap.
The final summary links to it; report version advances to 3 while SSH agent
protocol remains 2. Keep the existing 2 MiB summary report limit.

Record per-operation counts, expected rejections, failures, payload bytes and
exact nearest-rank p50/p95/p99/max latency. Attribute a request to the interval in
which it finishes. Window samples are detached under a short measurement mutex;
sorting and file writes occur outside that mutex. Keep aggregate samples for the
whole load, bounded by its operation ceiling. Flush a final window, fsync and
close the file when the load ends, including cancellation/failure when the stream
remains writable.
If an interval or interruption fails first, cancel traffic and wait for all
requests to finish recording before flushing the final window. With a writable
timeline, failure reports retain the same operation totals as their interval
rows. A timeline I/O or size-limit failure can prevent a final row; the failed
summary still retains whole-load counts and the error, with available rows left
for inspection.

Reuse the existing resource sampler instead of adding interval filesystem walks.
Each immutable observation includes its time, disk/metrics validity, errors,
current process RSS, file bytes, journal/replay bytes, applied/safe/base positions,
retained records and cleanup counters. A primary generation distinguishes
restarts between cycles. Discard stale/invalid observations from cleanup totals;
counter regression within a stable generation fails qualification. Sampled
counter differences provide observed cleanup totals and per-window record/blob
rates. They are primary observations; open readers may still hold unlinked blocks.
Window rates and ages are observations, not exact instantaneous disk throughput.

### Interruptions during traffic

With `--soak-faults=true` (default), partition private replication at one third
of the admission duration, then SIGKILL/restart the follower at two thirds. Hold
each interruption for one second by default; `--soak-fault-duration` ranges from
200 ms to two seconds. Customer requests continue. Replica durability retains its
normal response gating and deadlines; request errors remain failures.

Capture the applied sequence before and during each interruption using the
primary's direct private endpoint. Count actual customer HTTP starts and requests
still active, including response bodies, at the interruption boundaries. Require
live HTTP activity and a non-regressing journal position. A request may be gated
on a previously published, unconfirmed prefix; requiring another publication
during its pause would reject that valid durability behavior. Restore replication
and wait for the finite captured position to be reported by the registered follower. Replica mode also
requires the designated confirmation to reach it. Waiting for an ever-advancing
head to have zero lag could starve under sustained writes. After load drains,
the existing maintenance check still requires exact catch-up.

Record interruption timing, publication positions, completed workflow counts,
rejoin time and success/error. Schedule the second interruption after the first
has healed; keep interval reporting active during recovery. Join any started
interruption before returning from load. A restarted local worker inherits the
qualification lifetime, while its finite-position recovery wait has a separate
operation deadline; completing the interruption must not terminate the worker.
Cancellation restores the proxy or
attempts a bounded follower restart, then normal fixture cleanup stops/reaps all
children. SSH loss retains its existing EOF/Pdeathsig cleanup behavior.

### Passing evidence

A soak-specific named check requires the full requested admission duration,
fresh interval metrics, and at least two observed completed cleanup cycles when
automatic compaction is enabled. Requested interruptions must both show live
customer HTTP activity and successful finite-position rejoin. An early workflow ceiling
remains visible as `workflow_limit` and fails duration coverage; increase the
budget within the hard bounds or shorten the requested duration. Budget/probe
failures, cancellation, timeline errors and incomplete coverage write failed
reports. Disabling compaction or interruptions is recorded explicitly for matched
comparisons; it removes only the corresponding coverage requirement.

The original post-load checks remain mandatory, including historical positive
and negative outcomes, acknowledged bytes, capacity holds, corruption rejection,
restart, backup/restore, partition/rejoin and explicit planned promotion.
There is no hardware-independent latency pass threshold or production capacity
claim. Actual independent-host and independent-storage qualification still
requires configured disposable hosts and known backing storage.

## Validation

Test configuration bounds and explicit CLI precedence, lossless concurrent
interval aggregation, fresh disk observations, timeline permissions/replacement
and size bounds, counter baselines, cancellation/join, finite-position rejoin and
replica confirmation, customer activity/body/cancellation accounting, and
incomplete-coverage rejection. Require a restarted follower to survive completion
of the interruption context. Run the built processes
for full-duration enabled/disabled soak profiles in both durability modes, inspect
the JSONL rows and verify all post-load correctness/recovery checks. Exercise SSH
transport with the same profile while reporting shared-host scope honestly.

The 2026-10-17 matched built-process runs used a target of one workflow per
second, four workers, 60-second admission per mode and a 16-record cleanup
trigger. Enabled/disabled runs each completed 60 workflows per mode, both live
interruptions and all 13 checks per mode without unexpected request failures.
Enabled loads observed 95/143 cleanup cycles and 735 records/180 replay files
pruned in local/replica durability modes. Interval and aggregate counts agreed.
Sampled replay peaks were 10.1–10.5 MiB enabled and 159.1 MiB disabled; request
p95 increased in this shared-host pair. Other space triggers retained defaults.

A separate 30-second-per-mode OpenSSH run at the same target completed 30
workflows, both live interruptions and all 13 checks per mode. It observed 49/72
cleanup cycles and 372–374 records/91–92 replay files pruned. Matching protocol-2
agents and planned promotion also passed. Both participants shared a kernel and
filesystem; this is transport evidence, with physical failure domains unverified.

An initial unpaced four-worker local run exceeded the 30-second rejoin bound
under accumulated follower lag. Keep that failed evidence and the existing
deadline. Qualification also caught and fixed final-window ordering and local
worker-context lifetime bugs; regression tests cover both. These observations
establish development behavior at the configured target, not production capacity.
