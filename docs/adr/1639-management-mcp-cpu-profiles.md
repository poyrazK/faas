# ADR-1639: Read-only sampled CPU profiles through management MCP

## Status

Accepted. Extends the app-wide diagnostic consent in ADR-1618 and performance
guidance in ADR-1620 using the existing native profiling API. No migration,
collection, profiling enablement, plan, billing or native response change.
The catalog has 45 read/planning tools, 53 with writes enabled and six prompts.

## Decision

Add `gregale_get_profiles` with app, exact nonzero deployment UUID, required
native runtime and explicit ordered RFC3339 start/end at or before now. Runtime
is required by native validation; there is no default or latest deployment.
An optional route is the unchanged native `METHOD /static-pattern` or
`[unattributed]` label. Omission selects all routes. Reject unsafe selectors
rather than rewriting them. Normalize times to UTC without losing nanoseconds.
Native plan, retention and backend errors remain authoritative, with one native
profile read and no broadened retry or fallback.

Reuse diagnostic consent, app allowlisting and immutable app identity. Pin the
deployment's app ownership and scope before reading and recheck them and current
app consent/identity before release. The native profile response has no app ID;
validate its full echoed query and report `window_verification: native_query_echo`.
This verifies selection, not capture completeness.

Project selected sampled CPU seconds, the native nonzero sample-entry count and
a bounded prefix of native self-CPU-ordered function groups. Add default/max
function bounds of 10/50 and a 64-byte timestamp argument bound in limits.go.
This limit applies only to output, with native/returned counts and truncation;
there is no pagination or automatic follow-up. Preserve duplicate display names
and zero self CPU. Native groups use name/file/line identity, but project only a
bounded, redacted name, a modification flag and self/inclusive total CPU. Names
are untrusted display data, never selectors. Inclusive totals overlap across
caller groups and cannot be summed. Empty selection omits sampled CPU rather
than asserting observed zero. The count is not requests, unique stacks or
received profiles. These measurements cover sampled instrumented processes,
not VM CPU, billed usage, request latency or an established bottleneck.

Coverage and attribution have explicit `deployment_runtime_window` scope even
for route-filtered reads. Coverage describes receipt count, union of recorded
capture intervals, gaps, optional ingestion timestamp and recorded failed-upload
lower bound with completeness flag. Ingestion can occur outside the capture
window. A zero failure count does not establish lossless collection. Attribution
preserves total/attributed/unattributed CPU and percentages, native diagnostics
completeness and a bounded closed reason vocabulary. Unknown reason prose maps
to `unrecognized`. Neither quality summary establishes instrumentation coverage,
source completeness or freshness; those remain unknown. Missing/unavailable
quality stays explicit without discarding otherwise valid function evidence.
Reject inconsistent counts, query echoes, ordering and non-finite/contradictory
measurements, including malformed rows beyond the projected prefix.

Exclude flamegraphs, raw stacks/samples, file paths, line numbers, source links,
repository/commit provenance, route/request normalization tables, collector or
host identities and arbitrary native prose. Update browser consent disclosure
and performance guidance. Prompt retrieval fetches no profile evidence. No
additional environment/log/write access, model call, probe, upload, replay or
profiling collection is introduced.

## Consequences

Agents can inspect retained CPU hotspots and qualify what the measurements cover
without receiving raw profiles or turning collection on. Protocol tests cover
both supported MCP versions, consent and allowlists, deployment ownership/scope
and app replacement, native errors, exact selection, output bounds, duplicate
names, missing/empty quality and invalid measurements. Pinned real Codex checks
consent, restart/refresh, populated/empty results and zero native writes/model
requests. Portable fixture checks do not qualify native/provider deployments.
