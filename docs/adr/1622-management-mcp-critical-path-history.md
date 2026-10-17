# ADR-1622: Critical-path history through management MCP

## Status

Accepted. Extends ADR-1618, ADR-1619 and ADR-1620.

## Decision

Expose read-only `gregale_get_critical_paths` through the existing native
`GetAppDebugCriticalPaths` client. Accept app, optional positive lookback
duration/day alias, and an output pattern limit (default 5, maximum 10).
The limit only bounds the bridge projection in native order; it is not sent to
the API and does not change native sampling or aggregation. Native ownership,
debugger plan gating, 24-hour default and retention remain authoritative.
Return native errors without retries, broader windows or fallback queries.

Reuse app-wide diagnostic consent pinned to the reviewed app ID. Existing
request/performance consent, OAuth restart/refresh/revocation and local native
credential authority apply. Validate the native app ID and retained window;
recheck current app consent and identity after final shared authorization,
immediately before serialization. Do not grant environment, log or write access.

Project explicit DTOs for path patterns, ordered normalized segment identities,
dominant segments, weighted latency/error aggregates and selected request
examples. Native signatures are bounded display labels, not guaranteed unique
keys or supported filters. Omit raw spans, attributes, SQL/query payloads and
separate trace-ID fields. A native exemplar RequestID can itself be a trace
alias or telemetry-row ID; preserve it unchanged as `lookup_identifier` for
the existing app-scoped exact request tools. Reject unsafe or credential-shaped
lookup identifiers rather than redacting/truncating them into a different ID.

Centralize pattern, 16-segment and 3-exemplar bounds in `pkg/api/limits.go`.
Use the existing 256-byte text budget after known-credential redaction. Preserve
native source completeness/truncation, bridge path/segment/exemplar truncation
and text truncation separately. Projection completeness requires all these
bounds and source completeness; it never establishes full traffic capture.
Validate sample counts, weighted counts, nonnegative durations, finite rates
and factors, exemplar identifiers/timestamps and their labeled half-window.
Keep signed latency/error deltas intact. Empty paths remain explicitly empty,
including retained rows for which no path could be reconstructed.

Preserve the native statistical meanings. `path_samples` counts unweighted
retained rows with a reconstructed path. Calls and percentiles use represented
request weights, and path duration is reconstructed span-path wall time rather
than full end-to-end request latency. Aggregate dominant-segment exclusive time
is a maximum observed sample, not a percentile or typical bottleneck. Native
regressions compare halves of the read's time window, not deployments, and do
not establish causality. Exemplars are selected aggregate representatives, not
independent individual requests. Each evidence endpoint has its own window.

Extend `gregale_performance_diagnosis` to inspect native ordered critical paths
before using representative lookup identifiers or filtered request discovery
for exact evidence. Never manufacture paths by summing dependency percentiles.
Prompt retrieval fetches no performance evidence. Existing app/lookback prompt
arguments and consent-filtered completion remain unchanged.

## Consequences

Agents can connect sampled performance patterns to retained request evidence
without exporting raw spans or silently expanding diagnostic authority. No
native API, migration, collection, replay, probe, model request or platform write
is introduced. Remaining names are private/untrusted application data;
known-credential redaction is not a detector for every arbitrary secret.
Portable fixtures and pinned real Codex acceptance cover consent, redaction,
representative links, independent windows, empty/partial evidence and OAuth
restart/refresh/revocation. Native/provider deployment qualification is separate.
