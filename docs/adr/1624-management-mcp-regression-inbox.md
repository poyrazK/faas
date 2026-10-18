# ADR-1624: Read-only regression inbox through management MCP

## Status

Accepted. Extends ADR-1618–1623 and the native debugger regression API.

## Decision

Add `gregale_list_regressions` over the existing native GET endpoint, accepting
an app, optional positive duration/whole-day detection lookback (maximum 64
bytes), and output limit (default 20, maximum 50). Centralize output budgets in
`pkg/api/limits.go`. No new endpoint, schema, migration or triage write is needed.
Preserve native errors without retry, fallback or a broader query.

Reuse app-wide diagnostic consent pinned to the immutable reviewed app ID.
Validate projected deployment UUIDs and each deployment's app ownership/scope.
Deduplicate those reads and recheck ownership/scope after the shared final
authorization, with current app consent/identity last before serialization.
Only the projected prefix is dereferenced or released. Local stdio retains
native credential authority. Other environment, log and write permissions do
not expand.

Keep native visibility and ordering authoritative. The native query excludes
resolved and currently time-dismissed observations, normalizes expired
dismissals to active, and orders error-bearing observations before latency-only
ones, then by factor and detection recency. Do not locally filter lifecycle
state or timestamps against another clock. Preserve unknown future state/kind
labels as bounded untrusted data. Workflow state does not establish health.

The effective `since` filters `last_detected_at`, not request sampling time.
Preserve first/last detection and optional lifecycle timestamps; first detection
may predate the selected lookback. Keep the native one-hour default. Verify the
effective lookback is positive and at most requested/default; derive
`retention_clamped` with `requested_and_effective_lookback` provenance. The
native API supplies no exact window bounds, app identity echo, completeness
flag or pagination. Do not invent them. The exported app ID comes from the app
lookup, and projected deployments are checked against it. Report native and
projected counts, truncation, and `source_completeness: unknown`. Output limits
do not bound the native query. Empty results mean `no_visible_regressions`,
never proof of healthy traffic or complete capture.

Explicit DTOs expose native signal kind, p95/baseline, affected/error counts,
error rates, the preserved decimal factor string and triage state. Reject
invalid numeric/timestamp metadata. Metrics belong to the detector observation,
not a newly sampled comparison. Errors-only affected counts represent failed
requests. Project suspected dependency metrics as hypotheses, preserving the
legacy empty-reason meaning of latency and excluding SQL/query text by
construction. Do not infer a baseline deployment ID or root cause.

Redact known credentials before UTF-8-safe 256-byte text bounds. Strip route
queries/fragments and expose `route_modified` so altered display labels are not
silently reused as exact selectors. Application text remains private, untrusted
data; known-credential redaction does not detect all arbitrary secrets.

Extend `gregale_performance_diagnosis` with the inbox, request-evidence followup,
and comparison only when two exact deployment IDs are supplied. Keep prompt
arguments/completion unchanged and fetch no evidence during retrieval. Separate
detection recency from request windows; do not align them by inventing windows.
Do not acknowledge, dismiss, resolve, reopen, collect, replay or roll back.

## Consequences

Agents can prioritize native detector observations while preserving triage
visibility, sampling uncertainty and diagnostic consent. There are now 38
read/planning tools, 46 with write tools enabled, and six prompts. Protocol
fixtures and the pinned real Codex client exercise approved/denied access,
restart/refresh/revocation, empty evidence, projection limits and SQL omission
without model requests or native mutations. Provider/native deployment
qualification remains separate from these portable fixtures.
