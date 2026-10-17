# ADR-1552: Repeatable local object-service qualification

- Status: Accepted for development evidence; no production enrollment.
- Date: 2026-10-16
- Related: [ADR-1543](1543-owned-object-storage-write-receipts.md), [ADR-1544](1544-owned-object-storage-multipart.md), [ADR-1547](1547-owned-object-storage-backups.md), [ADR-1549](1549-owned-object-storage-primary-switchover.md), [ADR-1550](1550-owned-object-storage-replica-durability.md), [ADR-1551](1551-owned-object-storage-journal-compaction.md)

## Context

The object node now has receipt and multipart recovery, verified snapshots,
replication, planned authority transfer, optional replica acknowledgements and
offline replay compaction. Focused crash and gateway tests establish individual
boundaries. They do not provide a reusable mixed-load report linking throughput,
latency, resource use and repeated maintenance to end-to-end data and capacity
checks. Replay copying and hashing still execute under the catalog lock, and the
continuous catch-up worker polls once per second. Both can influence latency.

## Decision

Add `object-storage-qualify`, backed by `pkg/objectqualification`. Run explicit
built `object-storaged` and `object-storagectl` executables, recording their SHA256
identities. The controller launches a real node process and continuous follower
process, uses existing offline library APIs only while their process locks are
available, and routes AWS SDK requests through the real branded S3 gateway.
The gateway uses an isolated `state.MemStore`, synthetic policy and credentials;
its apid receipt and inventory recovery methods are the existing implementations.
The fixture clock advances only to make recovery leases/retries due.

Only new, private disposable directories are accepted. There is no existing-data
or remote-endpoint option. Generate independent operator and customer credentials,
replace inherited node credentials for child processes without dumping the
environment, and keep credentials out of JSON reports. Log children to private
files, stop/reap them on success, failure or cancellation, and keep artifacts for
inspection. There are no registry or customer provisioning changes.

### Workload and correctness model

Run local and replica durability modes independently by default. Each worker
owns stable keys, allowing concurrent operations on unrelated keys while avoiding
an ambiguous expected model. Coordinate DELETE phases against concurrent writes
and multipart sessions, respecting existing bucket-wide deletion admission. A
separate check requires refusal during an active multipart reservation and proves
no provider DELETE was dispatched. Deterministic seeded payloads vary by workflow,
including zero-byte writes. Exercise PUT/GET, overwrite, conditional rejection,
DELETE/missing GET, two-part multipart completion with a 5 MiB first part, and
multipart abort. Successful PUT responses must expose their gateway receipt ID.

Retain expected current sizes, SHA256 digests and ETags, deleted keys, and exact
positive/negative receipt identities. After load, restart and maintenance, verify
current bytes and ETags, absence of deleted objects, historical receipt proofs
and gateway settlement states. Run the real coherent inventory reconciler and
compare observed bytes/keys with the model, also requiring zero active multipart
reservation bytes. Historical receipts survive overwrite and deletion; missing
proof must never be treated as negative evidence.

Each cycle admits workflows for the configured duration or workflow ceiling,
then drains already started work before measuring its final elapsed time. Stop
admission on the disposable disk budget. Limits, defaults and timeouts live in
`pkg/api/limits.go`: default four workers, two cycles, two seconds per admission
window, 32 workflows per cycle and 64 KiB objects; maximum 16 workers, 16 cycles,
256 workflows per cycle, 8 MiB objects and 30 minutes of configured admission
time per mode. The sampled physical-file budget is 2 GiB per mode, with the
existing daemon free-space floor still enforced. One admitted workflow can
temporarily overshoot a sampled budget; this is not a filesystem quota.

### Explicit fault and maintenance scenarios

A stable local proxy changes only the transport target, preserving the signed
Host. It can block replication RPCs, discard one completed provider PUT response
and return 502, or close one PUT connection before forwarding it. These inject
uncertainty at known boundaries without editing service data or adding production
fault hooks. Count provider PUT attempts to reject recovery replay.

Verify that a lost acknowledgement leaves a dispatched gateway receipt and later
settles from the original proof after a private operator-adapter overwrite.
Customer admission remains pending during that overwrite; the operator adapter
lets the drill exercise historical provider proof without bypassing the customer
gateway's pending-write admission rules. A request never forwarded to the
provider has no proof and retains its capacity reservation through recovery and
inventory. In replica mode, partition replication, observe the local terminal
journal prefix through a separate authenticated status read, cancel the still
unacknowledged customer request, and prove receipt recovery stays pending until
catch-up confirms it. Local mode can acknowledge while replication is blocked.

Kill/restart a follower and publish a write while it is stopped; restart catch-up
and verify the write and all prior acknowledgements. Kill/restart the primary
and verify acknowledged data, receipts and capacity. Simulate storage pressure by
starting the disposable node with a free-space floor above available capacity;
its 5xx rejection must not release an admission without proof. This tests the
service's space-admission path, not kernel ENOSPC/EIO or real drive loss.

For maintenance, catch up, stop processes, compact both histories, create and
verify an offline backup, require a deliberately changed backup byte to fail
verification, restore the exact byte and verify again, restore into a new
non-serving follower, restart and rejoin. Require the compacted primary's fully
applied log and replay pool to be reclaimed. Finally perform the supported live
source fence and promotion, preserve the stable gateway backend identity, create
and bind a fresh successor standby when replica mode requires one, and verify
post-promotion writes and original proofs. No force promotion is introduced.

### Report contract

Write versioned JSON containing configuration, executable hashes, toolchain,
scope, per-mode results, timed correctness checks and per-cycle load measurements.
Count attempts, successful operations, expected rejections and unexpected errors
separately. Record successful payload bytes and exact nearest-rank p50/p95/p99/max
operation latency. Throughput uses the whole load interval including draining;
maintenance and fault durations are separate correctness checks. Do not invent a
hardware-independent latency SLO or turn measurements into implicit pass gates.

Sample child/controller RSS from procfs, physical file sizes, cached journal and
replay bytes, and the retained prefix held by replication. Report attempted and successful metrics/disk sample counts
and errors; require coverage of all process RSS and both sampling sources.
Sampled peaks are lower bounds, and probes can miss short events or
fail while a process restarts. Metric reads use bounded requests and perform no
custom disk reads inside the daemon. Controller file scans and proxy/gateway
overhead are part of this local setup.

A correctness or lifecycle error fails the run, exits nonzero and writes a
failure report. Reports are created exclusively with mode 0600 and never replace
existing files. Performance observations remain distinct from correctness.

## Consequences and validation

The same executable can reproduce mixed traffic and operational drills with
bounded data, seeded contents and inspectable JSON evidence. It can expose
throughput and latency differences across worker counts, object sizes and local
versus replica durability before changing publication ordering or lock scope.

Unit checks cover independent error/rejection counts, quantiles, repeatable data,
configuration bounds, existing-data/report refusal, fault targeting, child
credential isolation and process reaping. An opt-in integration test uses the
exact supplied binaries, runs both durability modes and requires workload,
correctness and resource coverage. Built CLI runs produce reviewable reports.

This remains shared-host, shared-filesystem process qualification with in-memory
control-plane state. It does not establish independent-drive failure protection,
PostgreSQL restart durability, WAN behavior, kernel I/O fault tolerance, billing
authority or full provider release qualification. Independent-host long-duration
runs and production enrollment retain their existing requirements. This milestone
makes no change to object publication ordering, acknowledgement semantics or SQL.
