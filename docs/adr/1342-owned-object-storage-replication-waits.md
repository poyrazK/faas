# ADR-1342: Bounded replication change waits

- Status: Accepted for development qualification; no production enrollment.
- Date: 2026-10-16
- Related: [ADR-1337](1337-owned-object-storage-replication-journal.md), [ADR-1338](1338-owned-object-storage-primary-switchover.md), [ADR-1339](1339-owned-object-storage-replica-durability.md), [ADR-1340](1340-owned-object-storage-journal-compaction.md), [ADR-1341](1341-owned-object-storage-local-qualification.md)

## Context

Continuous replication previously waited for a one-second ticker after every
bounded catch-up step, including steps with unapplied entries or incomplete
payloads. The local qualification run observed replica-mode PUT p95 around
1.1 seconds, consistent with that polling interval. Shared-host, small-sample
measurements identify an optimization to test, not its sole causal explanation
or a production latency objective. Immediate backlog draining also matters when
a payload takes several bounded transfer steps.

## Decision

Continue successful bounded catch-up immediately while behind. Once caught up
to the sampled applied head, issue an authenticated private GET with
`gregale-replication=wait`. Supply `stream`, `primary`, `epoch`, `sequence`,
`hash` and `revision`, all covered by the existing SigV4 query signature. The
cursor binds the sampled authority and applied prefix; the revision is a
versioned SHA256 of the relevant status projection. Reject duplicate or unknown
selectors, noncanonical numbers, bodies, conditional/range selectors and other
methods through the existing private endpoint rules.

The projection includes applied sequence/hash, role/handoff, authority, retention
floor, policy identity and follower registration generations. It excludes received
progress, timestamps, confirmations and reported follower positions. A worker's
own reports therefore do not trigger another catch-up cycle. Compare state and
capture the broadcast channel under the same short read locks. Release lifecycle
and catalog locks before waiting, and recheck the projection on every broadcast.
An event between catch-up and wait registration is detected by this comparison.

Return the current validated status on a relevant change or after a fixed
20-second server wait; an unchanged timeout is an ordinary 200 response. Unrelated
broadcasts never reset that bound. Existing HTTP requests time out after 30 seconds,
leaving response time beyond the idle wait. Limit each handler to 64 concurrent
wait requests, independently of upload slots; excess authenticated waits receive
the existing 503 capacity response. Cancel idle waits during daemon graceful
shutdown without canceling normal S3 requests.

Validate the requested prefix against retained history and the current source
authority. Wrong stream, primary, epoch or hash fails closed; a prefix below the
retained floor returns the existing 410 reseed error. Fenced primaries may still
export and wait on their sealed state. Check dirty authority/publication state
before returning success. This notification neither confirms a write nor gives
promotion authority.

After a notification or unchanged timeout, run the existing `CatchUp` path again.
It still fetches an authenticated head, validates authority and the hash chain,
downloads bounded verified ranges, publishes and fsyncs the local applied cursor,
and reports only that cursor. A notification response is not used as an apply
shortcut. Failed reports retry through the same path, including zero-data retries
after a lost acknowledgement. One-shot catch-up remains a single bounded step.
No on-disk format or SQL change is needed.

Transient transport errors back off for one second before resampling. Fork,
authentication/configuration, corruption and reseed failures stop the worker.
Only in-process sources lacking the optional wait interface retain idle polling;
the HTTP worker requires matching server support. Upgrade daemon and CLI together.
The worker continues holding its process lock across immediate steps and waits.

### Observability

Expose `faas_object_node_replication_waiting` and
`faas_object_node_replication_change_wait_seconds{outcome}` on the existing
private metrics listener. Outcomes are bounded to `changed`, `timeout`, `canceled`,
`error` and `rejected`; no stream, node, key or cursor labels are added. Existing
replica lag/response-wait and retention gauges retain their meanings.

Continuous progress JSON adds `wait_cursor` when caught up and a `follow` object
with cumulative change waits, elapsed wait milliseconds, retry/reconnect counts
and lag behind the sampled head. Counters cover completed prior work as of the
progress record. A reconnect means a successful catch-up after transient failure,
not a TCP connection count. Repeated warnings include retry/reconnect counts.

## Consequences and validation

Idle followers use a bounded outstanding request and respond promptly to new
applied data; backlogged followers retain work bounds without an artificial
pause. This does not move copying/hashing out of the publication lock, change
durability or receipt/capacity uncertainty, enable quorum or automatic failover,
or establish performance on separate machines and disks.
Immediate catch-up shares CPU and disk resources with foreground traffic. Local
qualification shows higher replica throughput alongside variable tail latency,
CPU cost and reduced local foreground throughput in matched samples. The
[runbook](../owned-object-storage.md) records those measurements and the failed
larger polling-baseline attempt; resource isolation and publication-lock tuning
remain separate work.

Race tests cover applied-only notification, report broadcasts which must not
self-wake, publication before wait registration, uncertain applied checkpoint
publication on either side of fsync, restart recovery, unchanged timeout, fencing,
shutdown, authority/hash mismatch and reseeding. Authenticated HTTP checks cover
selector validation, cancellation, wait saturation, graceful drain and invalid
source responses. Worker checks cover immediate partial-payload batches, idle
catch-up/confirmation, transient reconnect without recopying and permanent errors.
Rerun the bounded mixed-load qualification and recovery scenarios, and compare
latency/throughput with the same workload configuration. Performance numbers
remain observations, separate from correctness gates.
