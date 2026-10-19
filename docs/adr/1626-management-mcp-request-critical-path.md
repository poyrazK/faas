# ADR-1626: Per-request critical path through management MCP

## Status

Accepted. Extends ADR-1618–1625 and the native retained-span path reconstruction.

## Decision

Add an explicit `critical_path` projection to `gregale_get_request_evidence`
from its existing native response. Reuse app-wide diagnostic consent, immutable
app identity, deployment ownership/scope and final authorization. Add no tool,
prompt argument, API endpoint, migration or additional evidence query.

Preserve the native selected root-to-leaf parent chain. Validate bounded unique
native identities internally, ordered parent links, valid finite timestamps,
nonnegative wall/exclusive durations, exclusive at most wall duration, declared
count, chain wall duration and slowest-exclusive metadata. Bound validation to
the existing native 32-span budget; centralize the 16-segment MCP output budget
in `pkg/api/limits.go`. Contradictory paths yield `invalid_native_path` without
exporting segments, timing or links; other request evidence remains available.
An absent path is `unavailable`, and an empty valid envelope is
`no_retained_path`. Neither is a health assessment.

Replace every exported identity/link with a response-local JSON Pointer. Omit
trace/span IDs, names, dependency names, SQL, raw attributes and error messages.
Expose timestamps and wall/exclusive duration with closed reviewed span,
dependency and status categories. Unknown categories stay unknown; absent
dependency categories are unclassified. Normalize timestamps without inventing
ordering or containment across skewed parent/child clocks.

Report native reconstruction completeness independently from source-span and
projection truncation. Output completeness also requires retained root ancestry
and no source/projection truncation. Missing root ancestry carries no parent
pointer. Preserve native returned count, category limitations and unknown source
capture completeness. Export slowest-exclusive reference/metric only if its
verified native segment is in the prefix; do not replace an omitted maximum
with a smaller visible segment. Reconstructed path wall time is not full request
latency, and overlapping exclusive measurements are not additive. A path cannot
establish timings for every request represented by a collapsed telemetry row.

Extend structured explanations with `slowest_span` and `dependency_failure`
only when a unique retained span matches the exposed segment's ID, parent,
timing, duration and categories. Private lookup state never enters serialized
output. Handle the native `span:0` longest-span alias separately from literal
span IDs; require untruncated source and verified longest-duration ordering.
Dependency failure requires retained HTTP server-error evidence, a recognized
dependency category and an error on the matching exposed segment. Preserve
native confidence as an uncalibrated label; report observations, not root cause.
Omit unselected, contradictory or ambiguous support. Keep database/guest,
regression and comparison findings outside this projection.

Permit the existing native `inspect_dependency_failure` suggestion only with a
supported finding. Its fixed text points to already returned evidence and grants
no additional log/service access. No suggestion executes automatically. Update
request/performance guidance to explain local refs, timing semantics and limits.

## Consequences

Agents can inspect selected request timing and cite verified dependency errors
without exporting raw trace identities or native prose. The catalog remains
38 read/planning tools, 46 with writes enabled and six prompts. Protocol and
pinned real Codex fixtures cover consent, native absence, truncation, invalid
metadata, privacy, citations and zero model requests/native mutations. Portable
fixtures do not qualify native/provider deployment behavior.
