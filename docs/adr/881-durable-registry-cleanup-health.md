# ADR 998: Bounded durable registry cleanup health

## Decision

The existing authenticated, MFA-protected cleanup GET includes `discovery_health`
and `progress_health`. Reading health adds no bucket listing, aggregate root
scan, background task or mutation. Existing authoritative checkpoint and advisory
worker reads retain their timeout and failure behavior.

Discovery health describes the serving API process, separately for index discovery
and root reconciliation: last attempt completion, last successful page, next
scheduled attempt and consecutive discovery failures. Snapshot access is protected
by a mutex; it stores two lane records, resets on worker startup and marks shutdown.
It contains no scope identities, bucket keys, cursors or raw provider errors.
It is neither persisted nor a fleet aggregate. Routing a request to another node
can show a different observation.

Each lane is starting, healthy, retrying or stalled. A scheduled backoff is retrying;
stalled means no completed attempt by the scheduled time plus one minute, or no
initial attempt within one minute. Disabled and stopped workers are explicit.
Successful discovery does not assert that cleanup progressed or that a complete
scan finished. Per-scope failures do not increment discovery failures.

Progress health uses the selected scope's authenticated checkpoint and advisory
worker status. It reports unfinished operation age and idle, complete, reader_grace,
manual, observation_unavailable, awaiting_observation, in_progress, retry_wait,
ready or stalled. An active claim and scheduled retry suppress a stalled signal.
Otherwise the stall threshold is ten minutes after the later of reader grace,
last recorded success and next attempt. An absent observation does not prove a
stall: slow reconciliation and another node may explain it. Clock skew and manual
progress can make observations lag; operators must inspect the checkpoint before
acting. Signals authorize no automatic replay, cancellation or takeover.

Worker debug logs emit bounded page observations: scanned keys, authorized active
operations observed before stepping, oldest active start time in the page, attempts,
scope failures, obsolete hints pruned and hint publication failures. Active counts
include grace waiting. Hints pruned include already-absent idempotent deletes.
Logs contain no customer identifiers. Pages may overlap or be repeated across
nodes; counts must not be summed into an exact backlog. Empty and partial pages do
not prove there is no unfinished work. There is no global pending count or oldest
operation claim and no external notification integration.

## Validation

Remote race coverage checks independent lane backoff/stall transitions, recovery,
concurrent reads/writes, shutdown/restart, selected-scope grace/lease/retry handling
and authenticated response fields. Existing cleanup/index tests cover bounded
discovery, authorization, safe pruning and failure fallback. Native GCS/KVM
acceptance remains required before production enablement; this change does not
enable flags, deploy or create a PR.
