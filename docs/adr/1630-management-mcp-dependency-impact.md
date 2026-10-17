# ADR-1630: Dependency impact edges through management MCP

## Status

Accepted. Extends ADR-1620, ADR-1622 and ADR-1629 using native dependency history.

## Decision

Extend `gregale_get_dependency_latency` with a bounded `edges` projection from
the same native response. Add no tool, argument, native query, endpoint,
migration or permission. Existing app-wide diagnostic consent, immutable app
identity, native plan/retention checks and final authorization protect the whole
response. Follow-up request tools retain their own consent and deployment
ownership/scope rechecks; projecting an exemplar performs no follow-up query.

Cap the native ordered prefix at twenty edges and three exemplars per edge in
`pkg/api/limits.go`. Expose separate native/returned edge counts, edge truncation,
per-edge native/returned exemplar counts and exemplar truncation. Aggregate
exemplars_truncated describes only returned edge prefixes. Retain native source
complete/truncated flags separately. Existing complete becomes false for any
dependency, edge, exemplar or text projection limit. Add source_completeness
unknown; neither native nor bridge completeness proves capture of all traffic.
Missing edge arrays remain empty without inventing a reason or complete topology.

Explicitly project bounded from/to type/kind/name labels and child-span metrics:
weighted calls/errors, latency/exclusive percentiles and native split-window
trends. Labels_modified reports redaction/truncation/cleaning of either endpoint.
Labels are display data, not graph IDs, route selectors or unique identities;
different native nodes can look alike after projection. An application/request
parent can be synthetic because a span's original parent was missing or
unretained. Aggregated parent-child links do not establish a complete service
topology, causal path or deployment regression.

Edge calls count represented request weights for child-span samples; multiple
calls in one request can legitimately exceed the window's represented request
count. Edges and durations overlap and must not be summed. Native regressions
compare the two halves of this dependency window. The native edge DTO supplies
neither per-side call counts nor a failure-regression flag; do not manufacture
them from dependency nodes. Report side_sample_availability not_reported:
zero baseline/current scalars cannot distinguish absent side samples from
observed zero. A half-window exemplar supplies only that selected sample, not
side completeness or total counts. Retain signed metric deltas and validate rates,
counts and nonnegative duration metrics using the dependency contract.

Exemplars expose lookup_identifier from the native request selector, half-window,
timestamp, duration, HTTP status, child-span error and represented count. Report
duration_basis child_span_sample explicitly: exemplar duration is not full request
latency and a child error is independent of HTTP status. Selection is a bounded
current/baseline/failed representative, not a random independent request. Native
selectors can themselves be trace aliases; omit the separate trace ID. Never
rewrite, redact or fall back to another field to make a selector usable.

Require unchanged valid bounded selectors, positive count bounded by the edge's
calls/outcome counts and the window's represented requests, nonnegative duration, a valid
optional HTTP status and a timestamp inside its labeled half-window. Window start
is inclusive, end exclusive, with the midpoint in the current half. Reject
duplicate selector IDs within a projected edge; reuse across edges is permitted
and does not make independent evidence. Invalid exposed metrics/exemplars return
invalid_response, consistent with existing performance projections.

SQL/query patterns, raw spans/attributes, separate trace IDs and native deployment
comparison payloads remain excluded. Extend performance guidance with native
sampling, root-label, child-duration and safe exemplar follow-up semantics.
Retrieval performs no probes, collection, replay, model requests or writes.

## Consequences

Agents can move from dependency totals to retained relationships and representative
request evidence while preserving sampling and output bounds. There remain 38
read/planning tools, 46 with writes enabled and six prompts. Protocol and pinned
real Codex fixtures cover consent, redaction, empty evidence, representative
drill-down and zero model requests/native writes. Provider/native deployment
qualification remains separate from portable fixtures.
