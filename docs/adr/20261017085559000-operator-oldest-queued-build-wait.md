# Operator oldest queued-build wait

Status: accepted

## Context

The depth warning requires more than fifty queued builds. The existing p95 wait
histogram receives observations only when builderd starts work (including cache
hits). A queue of one can therefore remain stuck without either warning firing.
Spec section 12 supplies the five-minute wait warning budget. Section 17 and
Appendix D do not require admission, execution, or rollout changes for this
read-only observation.

## Decision

Extend the existing apid collector with a single SQLC aggregate returning count
and minimum original `enqueued_at` from rows whose status is `queued`. Both
values share one PostgreSQL snapshot or one MemStore lock. The existing
queued-only index bounds the scan to queued rows when the planner chooses it;
the existing three-second query deadline still applies. No new poller, database
write, schema change, or scrape-time query is introduced.

Use a separate narrow `BuildQueueObservationStore` contract. Legacy count-only
store adapters cannot supply coherent age evidence: they remain explicitly
unavailable, with the existing telemetry warning, rather than fall back to two
independent reads. Production PgStore and MemStore implement the contract. The
legacy admin endpoint's count method and response remain unchanged.

Export `faas_build_queue_oldest_enqueued_timestamp_seconds` with the count from
the same in-memory snapshot. Zero means an observed empty queue; failure,
expiry, or invalid evidence omits both gauges. Reject negative count, nonempty
queue with missing/nonpositive/future oldest time, and empty queue with nonzero
oldest time. Validate enqueue time against read completion, since a legitimate
enqueue can occur after the conservative read-start freshness timestamp.

Require the oldest timestamp and count to agree in the existing healthy-input
recording. Record `faas_build_queue_oldest_wait_seconds` as current Prometheus
time minus the oldest timestamp for a fresh nonempty queue, or zero for a fresh
empty queue. Missing evidence produces no wait series. Raw timestamps let wait
advance between polls. Each apid observes the fleet independently; never sum
their counts or borrow freshness or timestamps across instances.

Add internal warn `FaasBuildQueueOldestWaitHigh`: oldest wait strictly greater
than 300 seconds for ten minutes, regardless of depth. Preserve the existing
depth and p95 thresholds and holds. Failed or expired evidence resets the hold;
the independent telemetry warning tracks that unknown interval. Recovery to an
old queue starts a new hold rather than proving continuous observation.

Requeue preserves original enqueue time. Claiming removes a row from the queued
observation; cancellation/completion excludes it, and removing the oldest row
reveals the next oldest. A queue observed empty during a claim resets the hold.
This measures currently queued work, not running-claim stalls, builder liveness,
or proof of a failed builder. It performs no remediation or admission changes.

## Validation

Test coherent count/oldest snapshots on PostgreSQL and MemStore through repeated
claim/requeue, oldest cancellation, running/succeeded/failed exclusion, drain,
and new work. Collector race tests cover invalid pairs, missing adapters,
deadline, failure, expiry, concurrent scrapes, and enqueues during reads.
Promtool fixtures exercise a single stalled build with no start histogram,
strict five-minute boundary and ten-minute hold, growing age, empty/unknown,
replica isolation, cancellation, stopped polling, and recovery. Regenerate both
rule catalogs and retain a zero-exception metric-emission inventory.
