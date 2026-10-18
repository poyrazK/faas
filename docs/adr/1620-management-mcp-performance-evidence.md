# ADR-1620: Retained performance evidence through management MCP

## Status

Accepted. Extends ADR-1618 and ADR-1619.

## Decision

Expose read-only `gregale_get_debug_coverage` and
`gregale_get_dependency_latency` using the existing native app-scoped debugger
client methods. Accept app and optional lookback only, retaining the strict
positive duration/day syntax and 64-byte bound from ADR-1619. Native 24-hour
defaults, ownership, plan and retention enforcement remain authoritative.

Reuse per-app diagnostic consent across environments. Existing app/environment,
log and write permissions are not expanded. Pin the selected app ID before the
read, validate the native response's app ID and window, and recheck current
consent/app identity after final shared authorization before serialization.
Native errors are returned without fallbacks or broadening the query.

Project coverage counts and trace/span/wake/guest signals explicitly. Validate
nonnegative counts, retained denominators, finite percentages and observation
timestamps. Percentages apply only to retained represented requests; storage
cannot establish how much traffic was never captured. Always report collection
completeness as unknown. Preserve empty evidence explicitly rather than treating
zero percentages as health or full capture.

Preserve separate native effective lookbacks/windows for coverage and dependency
reads. Dependency retention-clamping comes from its native flag. The native
coverage DTO has no such flag; derive its bridge indicator by comparing requested
(or native default) and effective lookbacks, and label that derivation. These are
independent sequential observations, not an atomic snapshot.

Project at most 20 dependency groups in native order, using the centralized
`MCPPerformanceDependenciesMax` in `pkg/api/limits.go`. Bound string fields to
the existing 256-byte diagnostic budget after credential redaction. Include
percentiles, calls/errors, native split-window latency/failure regressions and
sample/represented-request counts. Preserve native source completeness and
truncation separately from bridge group/text truncation. Projection completeness
requires both source and projection completeness; it never means full capture.
Exclude SQL/query patterns, raw spans/attributes, bodies/headers, dependency
edges and deployment-comparison payloads. Dependency timings may overlap and
cannot be summed as a critical path. Native time-split regressions do not prove
deployment regressions, causality or root cause.

Add `gregale_performance_diagnosis` with app and optional lookback. It instructs
the agent to inspect coverage/dependency samples before discovering relevant
requests and drilling into exact evidence. Dependency names are not route filters.
It retrieves no performance evidence and does not approve plans or writes.
Completion suggests consented current app identities only, never evidence or
lookback values. Existing OAuth restart/refresh/revocation fences apply.

## Consequences

Agents can investigate app-wide performance with honest sampling and retention
limits. No new migration, native API, collection, replay, probe, model call or
platform mutation is introduced. Known-credential redaction does not remove all
arbitrary application secrets; remaining text is private/untrusted data.
Portable fixtures and the pinned real Codex client verify the bridge's consent,
redaction and empty/partial-evidence behavior. Provider/native deployment
qualification remains separate.
