# ADR-1631: Route request analytics through management MCP

## Status

Accepted. Extends ADR-1618, ADR-1619 and ADR-1620 using the existing request
analytics API; no native endpoint, state change or migration.

## Decision

Add the read-only `gregale_get_request_analytics` tool. Reuse per-app diagnostic
consent and immutable app identity, including a final authorization/identity
check before returning evidence. Native ownership, plan and retention checks
remain authoritative. Read the existing app analytics endpoint once with
`group_by=route`; do not expose arbitrary grouping or fetch request examples.

Accept positive duration/day or RFC3339 `since` and optional historical RFC3339
exclusive `until` at or before now. Default to native 24h; normalize timestamp
offsets to UTC. Bound time strings and output route prefixes in
`pkg/api/limits.go`: 64 bytes, default twenty routes, maximum fifty. `limit`
changes projection only and is not forwarded as native pagination/query size.

Preserve native effective since/from/until, as_of and window_clamped. Require
the selected slug/grouping, positive matching duration, coherent half-open
window and native clamp metadata. Reject changed historical ends, widened
windows or absent metadata without fallback. Native retention caps the duration
ending at the selected until, not data availability at a historical time.
Report source_completeness and freshness unknown; native as_of is query time,
not a measurement of telemetry freshness or complete traffic capture.

Explicitly project summary and route/method counts, HTTP error rate, cold boots
and p50/p95/p99. Error basis is HTTP status >=400 (4xx and 5xx), distinct from
5xx-only debugger regression signals. Counts and latency percentiles are weighted
by collapsed retained request counts. Preserve observed zero latency with a
positive count; omit percentiles with latency_status no_retained_requests when
no requests remain. Empty output does not prove health. Summary and route SQL
reads are sequential; do not require their counts to reconcile.

Project optional native cold-request/wake-boot p95, guest execution p50/p95,
CPU average/p95 and maximum RSS. Preserve absent versus observed zero; expose
measurement_coverage not_reported because the route DTO has no measurement
counts. Retained values may be rounded/bucketed. Guest execution is runner wall
time. CPU covers supported one-shot child processes including startup; RSS is
their rounded/bucketed process high-water mark, not VM/app allocation. Cold
request duration includes handling; wake boot duration covers scheduler boot
start to RUNNING. Their sample populations differ: never subtract/sum
percentiles, infer coverage or derive billing/resource recommendations.
Validate exposed counts, rates, percentile order and nonnegative measurements.

Keep native ordering, route limit/count and groups/routes truncation flags
separate from projected route count, prefix truncation and text truncation.
Redact credentials before bounding labels to 256 bytes; remove query/fragment
from routes. Modified labels can collide. Report route_modified,
method_modified and route_filter_usable. Only unchanged safe route selectors
can be used for independent discovery reads; discovery has neither a method
filter nor historical until. Do not remove method prefixes or rewrite labels
to claim the same cohort. Retain independent window and scope limitations.

Exclude native groups/dimension identities, compute/deployment estimates,
deployment observations/provenance, dependency payloads and future native DTO
fields through explicit projection. No additional permissions, environments,
logs, probes, collection, replay, model requests or writes are introduced.
Extend performance guidance without fetching analytics during prompt retrieval.

## Consequences

Agents can prioritize retained route investigations using traffic and optional
runtime measurements without treating missing evidence as healthy traffic or
resource advice. There are 39 read/planning tools, 47 with writes enabled and
six prompts. Protocol and pinned real Codex fixtures cover consent, historical
windows, projection/privacy, zero versus missing measurements, empty evidence,
restart/refresh and zero model requests/native writes. Portable fixture
acceptance does not qualify a native/provider deployment.
