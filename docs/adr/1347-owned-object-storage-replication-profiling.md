# ADR-1347: Profile replication and fetch bounded journal pages

- Status: Accepted for development qualification; no production enrollment.
- Date: 2026-10-17
- Related: [ADR-1337](1337-owned-object-storage-replication-journal.md), [ADR-1339](1339-owned-object-storage-replica-durability.md), [ADR-1343](1343-owned-object-storage-publication-preparation.md), [ADR-1345](1345-owned-object-storage-automatic-compaction.md), [ADR-1346](1346-owned-object-storage-soak-qualification.md)

## Context

The one-workflow/second development soak passed, while four unpaced workers
accumulated follower lag and exceeded the existing 30-second rejoin deadline.
Before raising work budgets or changing durable checkpoints, distinguish source
fetch time from byte pacing and local durable work. A profiled 60-second local
trial stopped after 50 seconds at that recovery deadline: 1,088 records applied
in 47.16 seconds of observed catch-up attempts. Entry fetches accounted for
30.9% of attempt time and byte budget waits for 12.5%. Primary traffic published
roughly 60 records/second while the follower applied roughly 23. This is a
shared-host observation, not an independent-device capacity measurement.

## Decision

### Observe bounded attempts

Add an additive `profile` to catch-up progress. Record wall time for source/head
position checks, entry fetching, durable journal reception, download, download
verification, durable catalog application, durable applied checkpoint and cursor/
confirmation reporting. Verification is a subset of download time. Phases
include their budget waits; the separate byte/entry wait fields must not be
added to phase totals. Entry reservations occur within entry-fetch time.
Total time covers the catch-up attempt under the follower catalog lock; it
excludes waiting to acquire that lock, between-step maintenance and idle change
waits. Durable phase timings include metadata, copying and fsync; they are not
raw filesystem/device fsync latency or CPU profiles.

Continuous CLI progress includes an observation timestamp and an optional error
for failed catch-up attempts, retaining any durable local checkpoint and work
observations. These records confer no additional durability proof. Idle change
waits and reconnects retain their existing separate telemetry. A killed process
may leave an attempt without a final observation.

The local/SSH qualification agent captures each follower process's stdout to
an owner-only `follower-progress-*.jsonl` file. Reuse the soak limits of 16 MiB per
file and 32 KiB per row. Preserve complete rows across pipe writes; append an
explicit `{"truncated":true}` marker and discard subsequent progress at either
bound. Capturing a partial row at process death does not publish that row.
Truncation does not stop replication. Actual capture I/O failures remain visible
through child-process health checks. Remote files stay in the remote private
fixture directory. These logs are observational and are included in sampled
fixture disk usage; report version 3 and SSH agent protocol 2 remain unchanged.

### Fetch pages, apply individual records

Add an optional `ReplicationBatchSource` and authenticated private `entries`
read with canonical positive sequence/limit parameters. A primary status response
advertises support through `X-Gregale-Replication-Entry-Batch: 1`. The HTTP client
uses single-entry requests when support is absent; it never downgrades after a
failed or corrupt advertised page. Existing single-entry APIs remain supported.
No storage format or durable status field changes.

A page contains at most the existing 32-entry step bound and at most 4 MiB of
canonical JSON, with two active page responses per handler. These limits live in
`pkg/api/limits.go`. Read a bounded contiguous prefix under one primary catalog
read lock, then release that lock before sending. Keep page response slots
bounded through response writing. One candidate record may be read beyond the
remaining page space, up to the existing 2 MiB record limit, and omitted.
Keep these slots
separate from payload transfers, status, cursor reports, confirmations and
change waits. Excess page requests receive the existing authenticated capacity
response. Decoder byte and element limits apply before allocating an unbounded
array. Duplicate/unknown fields and ambiguous encodings remain invalid.

The follower requests only the captured head's remaining prefix and the current
step's remaining entry budget. Reserve entry-start tokens for the requested
page before fetching; reservations persist across steps/reconnects. Short pages,
partial payload transfers and failed attempts can consume unused reservations
conservatively. Existing byte-work accounting, 64 MiB/s and 256-entry/s defaults,
maxima and bursts remain unchanged. Legacy peers reserve one entry at a time.

Validate the complete returned prefix before receiving its first record: count,
encoded bytes, canonical records, stream, epoch, contiguous sequence/previous
hash and the captured head hash when reached. Empty, oversized, reordered,
gapped, foreign or corrupt pages fail closed. Pages may end early at their byte
bound, and catch-up can fetch another within the same step bounds.

Keep prefetch only in bounded memory for the attempt. It does not advance
received/applied state, authorize pruning or confirm data. For each record retain
the existing durable journal/received checkpoint, verified resumable payload,
catalog application and fsynced applied checkpoint. Received remains at most one
ahead of applied. On failure or a byte-bounded partial step, discard unused page
records and fetch them again after reconciling durable state. Report only the
applied cursor, with the existing mandatory replica confirmation and historical
receipt/capacity behavior. No customer write replay or automatic failover is
introduced.

## Validation

Require invalid later entries to reject a whole prefetched page before receiving
its first record. Exercise byte-bounded short pages, element allocation limits,
canonical decoding, authenticated/legacy fetch paths, independent response slots
and slot release after response-write errors. Inject failures before/after
received, catalog and applied persistence with pending prefetched records; reopen
and verify contiguous replay and applied-only confirmations. Cover partial
payloads, persistent entry pacing, failed-attempt profiles and bounded chunked
progress capture. Run existing receipt, multipart, compaction, promotion,
durability and process-death checks plus increasing-load built-process runs.

Keep failed profiles and the unchanged recovery deadlines. Compare offered load,
request latency, applied rate, lag and recovery at identical work budgets. Local
results do not establish independent-host capacity. Backlog admission policies
and host/device isolation require separate design and evidence.

### Development measurements

The 2026-10-17 sequential shared-host trials used four workers, 64 KiB objects
with the existing 5 MiB multipart mix, seed 1, a 16-record compaction trigger,
60-second admission and unchanged 64 MiB/s / 256-entry/s work budgets. Profile
analysis selected only complete attempts within admission and checked phase
totals and applied-only positions. Active attempt replay increased from 23.07
to 34.23 records/second in the unpaced comparison; entry fetching fell from
30.9% to 2.6% of observed attempt time. This excludes lock acquisition,
between-step maintenance, startup and idle waits, and is not a wall-clock service
capacity limit.

The unpaced baseline timed out on its first partition/rejoin at 50 seconds.
Batching completed 280 workflows admitted over 60 seconds and all 13 checks.
Partition recovery took 14.8 seconds and follower restart recovery 28.0 seconds;
the latter completed at about 69 seconds, after admission stopped. Sampled lag
still grew to 1,479 records at 60 seconds. Successful bounded recovery therefore
does not establish that unpaced traffic is sustainable. Request p95 also rose
in this comparison, which admitted different workflow counts and ran for
different observation lengths.

At a fixed target of two workflow starts/second, local before/after trials each
completed 120 workflows and all 13 checks. Sampled whole-mode peak pinned-record
lag fell from 126 to 49. PUT p95 was 87.8/84.8 ms and GET p95 23.4/17.9 ms;
partition/restart rejoin improved from 3.80/2.00 seconds to 1.00/1.04 seconds.
The replica-durability trial at target two completed 116 workflows and all
13 checks; gating and finite workers can underfill the target.

Local targets three/four completed 165/210 workflows, with peak sampled lag
154/775 and late-window lag 47/762 respectively. Both passed all 13 checks, but
the higher target grew retained backlog. This workload retains useful measured
headroom at target two; it does not justify a production default or promise
three/four workflow starts per second. Every successful report had zero
unexpected customer request failures and consistent interval/aggregate totals.
Preserve the failed baseline and exact binaries/configuration alongside the
passing results. Independent-host/storage qualification and backlog admission
remain pending.
