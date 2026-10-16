# ADR-1476: Isolated MCP activity watch admission

Status: Accepted

## Context

Raw activity and named-consumer watches can wait for up to 45 seconds. Sharing
the eight ordinary-request slots lets waiting watches block acknowledgments,
consumer inspection, resources and completions. Rollout observations already
have an independent pool, but admission's boolean classification cannot express
a third class or report which pool is saturated.

## Decision

Use a closed admission class set: short_requests, rollout_watches and
activity_watches. Keep eight ordinary-request and two rollout-watch slots; add
a separate pool of two concurrent activity watches. Both
`gregale_watch_environment_cache_schedule_activity` and
`gregale_watch_environment_cache_activity_consumer` use that same activity pool.
Classify exact registered names, not name prefixes or caller-provided labels.
Other tools, resource/prompt/completion requests and subscription observations
continue using short capacity. Centralize all three bounds in pkg/api/limits.go.

Admission remains per management connection / remote OAuth grant, shared across
its SDK sessions and HTTP requests. Separate grants have independent capacity.
Check the caller context before admission. Do not queue or perform native I/O
for rejected work: return existing mcp_busy / diagnostic 429 / one-second retry
guidance. Release the selected reservation using the handler's existing deferred
cleanup on success, validation/native errors, timeout, cancellation and drain.
No consumer progress is committed by admission itself.

Extend gregale_management_mcp_active with fixed kind activity_watches, measuring
the entire handler reservation. Add
`gregale_management_mcp_admission_rejections_total{kind}` for full-pool attempts,
initialized to zero for all three classes. Skipped subscription reads count in
short_requests because they use that pool. Do not count cancellation before
admission, invalid internal classes or lifecycle rejection as saturation. Existing
request busy outcomes retain fixed registered-tool attribution. No identity,
consumer, target, URI or arbitrary error labels are added.

## Consequences

Acknowledgment/read capacity and rollout observation capacity remain available
when the activity pool is full, and activity watches can start while short
capacity is full. This reserves capacity, not latency or native-service health;
orthogonal ordinary-request saturation can still reject an acknowledgment.
Activity concurrency changes from sharing up to eight short slots to two
combined raw/consumer observations; clients handle overload using existing retry
guidance. A connection can reserve up to twelve handlers across the three pools;
idle subscription streams have their existing independent bounds. There is no
new process-wide or fleet-wide quota.

This supersedes ADR-1475's use of short-request admission for consumer watches.
No tool catalog, native API, journal format, database migration or model turn is
required. Portable in-memory and Streamable HTTP tests verify mixed-watch pool
sharing across SDK sessions, rejection before native I/O, successful ACK/get/list
under activity saturation, separate connection and rollout capacity, inverse
isolation when short capacity is full, and cleanup/reuse after empty timeouts,
native failures, cancellation, parent deadlines and drain.
