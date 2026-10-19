# ADR-1627: Guest execution evidence through management MCP

## Status

Accepted. Extends ADR-1618–1626 and the native runner execution-evidence contract.

## Decision

Add a bounded `guest` summary to the shared request projection used by exact
request inspection, request evidence and discovery. Use the already returned
native request row, without new queries, tools, prompt arguments, endpoints,
migrations or diagnostic permissions. Existing app-wide consent, app identity,
deployment ownership/scope and final authorization apply to the whole response.

Expose only reviewed runtime, outcome and error-class values. The native runtime
vocabulary includes node22/node24, python312/python313, go124 and binary. Keep
runner outcomes independent of edge HTTP status; a recorded timeout or handler
error need not be inferred from an HTTP server error. Map unreviewed text to
unknown, absent error class to not_reported and missing evidence to unavailable.
No payload, exception message, stack trace, CPU/RSS data or raw headers cross
this projection. A journal-only alias or row lacking a positive represented
count cannot establish execution timing.

Centralize the transport duration maximum of 86,400,000 ms in
`pkg/api/limits.go`, matching existing gateway/runner validation. Preserve
observed zero using an optional scalar. Invalid duration or a contradictory
known outcome/error-class pair yields invalid_native_guest with no duration;
other request evidence stays available. An omitted optional error class does
not contradict a known failed outcome. Unknown categories yield partial
evidence and cannot support a guest failure finding.

The telemetry publisher can quantize guest duration to a bucket upper bound,
including rows representing one request; exact-preserved and bucketed rows are
not distinguished in the API. Report native_retained_may_be_bucketed precision
and represented-count uncertainty. Do not infer exact individual execution
time, full request latency, additive phase timing or an execution for every
request represented by a collapsed row. Native outcome/class grouping is
preserved by the publisher; no new bucketing or aggregation occurs in MCP.

Extend approved explanation codes with guest_failure only when the exposed
summary is supported, internally consistent and reports a known failed outcome.
Resolve the canonical native guest label to /request/guest. Keep fixed
descriptions, native rule confidence and approximation for collapsed rows;
do not claim calibrated probability or root cause. Omit missing, successful,
unknown, malformed or unsupported references. Do not manufacture a finding
when native synthesis is absent.

The existing inspect_guest_failure suggestion prefers an available guest
finding over a general HTTP failure and otherwise retains its supported
request-level fallback. Fixed text grants no log/environment access and
executes no action. Extend request/performance guidance to explain these
semantics without fetching evidence during prompt retrieval.

## Consequences

Agents can distinguish retained runner timeouts, handler failures and HTTP
outcomes while preserving missing evidence and coarse duration precision.
There remain 38 read/planning tools, 46 with writes enabled and six prompts.
Protocol and pinned real Codex fixtures cover consent, missing/unknown data,
malicious category text, duration validation, independent HTTP/runner outcomes,
resolvable citations and zero model requests/native writes. Provider/native
deployment qualification remains separate from portable fixtures.
