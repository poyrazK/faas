# ADR-1636: Request-to-wake drill-down through management MCP

## Status

Accepted. Connects ADR-1618/1619 request diagnostics/discovery to ADR-1635 exact
wake timelines using the existing native request WakeID. No native API, schema,
persistence, migration, VM lifecycle or deployment change. Catalog remains
44 read/planning tools, 52 with writes enabled and six prompts.

## Decision

Expose a nested wake association in every projected request: discovery rows,
exact reads and request evidence. Basis is native_retained_request_wake_id and
association_scope retained_row. A usable retained row with a safe nonempty wake
ID reports observed, preserving the ID unchanged and providing timeline_tool
gregale_get_wake_timeline plus explicit app/wake_id/default-50-limit arguments.
No new budget is required; reuse the timeline identifier and event limits.

Validate wake IDs with the standalone tool's selection checks: path-safe,
bounded, credential-free and unchanged. Reject selectors that would be altered
by redaction. Withhold only invalid optional wake metadata as invalid_native_wake;
preserve the request's other evidence. No native ID reports not_reported. Missing
retained row identity, journal-only evidence or nonpositive count reports
unavailable. These states expose no ID, tool or follow-up arguments, and establish
neither warm execution nor no wake, failure or health. A cold-boot flag cannot
provide an ID, and a recorded ID cannot establish cold_boot.

Expose represents_multiple_requests and preserve native represented counts. The
current native collapse key includes wake ID, but these remain retained-row
associations rather than exact individual executions or complete request-to-wake
membership/timing records. Do not turn shared wake observations into independent
per-request samples, sum/subtract durations or infer root cause.

Always report timeline_availability not_checked, including when reduced request
timeline events exist. The bridge issues no extra wake-timeline API calls while
projecting requests. Standalone event retention, entitlement/MFA, ownership and
diagnostic consent remain independent. Following arguments is a new explicit
read with immutable app checks; arguments are data, not an authority token.
Preserve native missing-event errors without fallback IDs or broader queries.
Never infer a since/until/cursor from request receipt time or lookback.

For an already-supported native inspect_wake_timeline recommendation backed by
the exposed cold_boot finding, attach safe tool/arguments and cite /request/wake
when that association is observed. Do not invent a finding/recommendation from
the association alone. Missing or invalid associations retain the generic
returned-timeline/correlation suggestion without arguments. All citations stay
response-local JSON Pointers. Extend request/performance guidance and the native
timeline selector description; prompt retrieval reads no diagnostic evidence.

## Consequences

Agents can choose a request, inspect its recorded wake association and explicitly
read startup events without reopening discovery or guessing an identity.
App-wide diagnostic consent and final identity checks cover both steps; no
additional environment/log/write access, automatic enrichment, probes, collection,
replay or mutations are added. Instance/node/trace and other excluded payloads
remain excluded. Native timeline pagination/timing uncertainty is preserved.

Protocol and pinned real Codex fixtures cover discovery/exact/evidence handoff,
safe versus missing/invalid/journal associations, aggregate and cold-boot limits,
identity replacement, independent event errors, supported recommendation links,
restart/refresh and denied consent with zero model requests/native writes.
Portable fixtures do not qualify native/provider deployments.
