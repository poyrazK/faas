# Operator build queue observation

Status: accepted

## Context

`FaasBuildQueueDepthHigh` reads `faas_build_queue_depth`, but no producer exported
it. The admin builder-heartbeats endpoint already counts fleet builds in queued
state. Its response is not Prometheus input. Scrape success alone cannot prove a
background database observation is current. Spec section 12 requires observable
build queue health; section 17 and Appendix D do not require lifecycle or build
admission changes for this read-only telemetry.

## Decision

One apid-owned collector reads `QueuedBuildsCount` immediately and every thirty
seconds, sequentially, with a three-second context deadline. The existing count
query moves into SQLC without changing its queued-only semantics. An append-only
migration adds a queued-only index so periodic counts can avoid scanning
historical builds. It does not
count running or terminal builds, infer builder liveness, or attribute work to a
node. The store must honor cancellation; the collector never spawns replacement
queries for an unfinished read. The deadline bounds each observation, not the
number of queued rows the database must count.

Export one coherent in-memory snapshot as three unlabelled gauges:

- `faas_build_queue_depth`: the successful count, including a genuine zero.
  Omit it before success, immediately after failure, and when the observation
  is older than 120 seconds or in the future.
- `apid_build_queue_collection_success`: whether the latest completed read
  succeeded. Zero initially. Freshness remains a separate requirement.
- `apid_build_queue_collection_last_success_timestamp_seconds`: the last
  successful read's start time, zero initially and frozen on failure. A late,
  cancelled, or negative result cannot update the snapshot.

Scrapes only copy memory and never query Postgres. Each apid observes the same
fleet independently; do not sum replicas or let one collector's evidence repair
another's. One runner owns each registered collector. Process restart resets
observations to unknown.

A recording rule requires all three gauges, a successful apid scrape, successful
read, nonnegative depth, and a nonzero, nonfuture read at most 120 seconds old.
Join on the standard Prometheus `job,instance` scrape identity. The existing
depth alert keeps its strict >50 threshold and ten-minute hold, and now requires
that complete evidence. Failure resets its hold. A separate internal warn alert
reports a successfully scraped apid without a complete fresh queue observation
for five minutes, including missing metrics after an incomplete upgrade. Failed
scrapes remain the responsibility of existing daemon-down monitoring. Absence
of a configured apid scrape does not establish an expected installation.

No lifecycle, quota, admission, public API, frontend, or rollout changes.
Queue wait latency and builder heartbeat evidence remain distinct signals.

## Validation

Exercise startup, empty queue, backlog, failed and negative reads, frozen and
future observations, deadline/cancellation, single-runner ownership, scrape
isolation, serial ticks and recovery with Go tests and the race detector.
Promtool fixtures exercise real rule evaluation, strict thresholds, production
hold times, missing inputs, per-target joins, replica isolation, and recovery.
The static metric-emission inventory must need no missing-input exceptions.
Regenerate both operator rule catalogs and verify metadata and runbook links.
Apply the index migration locally and verify the count against PostgreSQL and
MemStore across queued, running, succeeded, failed, and cancelled builds.
