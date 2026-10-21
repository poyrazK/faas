# ADR-1633: Grouped error triage through management MCP

## Status

Accepted. Extends the diagnostic consent and evidence workflow of ADR-1618,
ADR-1619 and ADR-1631 using existing native error APIs and typed client methods.
No native endpoint, persistence or migration change.

## Decision

Add read-only gregale_list_error_groups and gregale_list_error_requests. Reuse
app-wide diagnostic consent across environments, live native ownership/policies
and immutable app checks before/after retrieval. Read exactly one native page
per call. Export no sample messages, headers, stacks, bodies, SQL/spans,
infrastructure/region or deployment/commit/image payloads. Explicit DTOs exclude
future native fields; exporting request IDs performs no debugger enrichment.

The summary accepts bounded RFC3339 since/until, not lookback aliases. Resolve
now/24h defaults once, normalize to UTC and truncate to PostgreSQL microsecond
parameter precision before querying. Reject collapsed ranges. Return normalized
requested filters and native effective windows, clamp flag and generated_at.
Validate app ID/slug, coherent requested/effective windows and native limits.
Summary window bounds are inclusive and filter group last_seen_at, not individual
error times. Count is the stored issue/merge counter, request_count the native
linked-request counter; both describe stored group history, not selected-window
totals, unique incidents, current retained rows or rate denominators. First-seen
may predate the window. Preserve actual SQL order: count, last-seen, fingerprint
all descending, rather than outdated ascending-fingerprint SDK comments.

Drill-down accepts an exact lowercase SHA-256 fingerprint and independently
reads retained fingerprint history. It has no summary-window filters. Project
public request lookup identifiers, receipt time, route, status and class. Validate
canonical nonzero UUID identifiers without rewriting them; classify lookup basis
public_request_id and evidence availability not_checked. Native debugger reads
remain independent explicit calls with their own retention, entitlement and
scope checks. ID-only/missing telemetry does not disprove a recorded error.
Preserve receipt-time/request-ID descending order. An empty terminal page may
omit class/route/status; report header_status not_reported rather than fabricated
HTTP zero. Initial missing/purged fingerprints retain their native errors.

Centralize native page defaults/maxima (20/50), timestamp bound (64 bytes) and
continuation-envelope bound (8192 bytes) in pkg/api/limits.go. Native full-page
continuation hints can legitimately lead to empty terminal pages. page_complete
means no native continuation hint, not complete capture. Both source completeness
and freshness remain unknown; generated_at is query time. Concurrent updates
can move mutable groups across pages; do not claim snapshot pagination.

Native summary cursors carry seek tuples, not time windows. Wrap the opaque,
unchanged native cursor in a bounded versioned MCP context envelope containing
kind, app slug/ID, page size and normalized requested/effective summary windows
or request fingerprint. Return next_arguments for exact continuation. Reject
context changes before the diagnostic read, recheck live app identity and reject
changed native effective windows without retries/fallback. The envelope is data,
not an authorization token; no permission derives from it. Never decode/rewrite
the native cursor payload or use it as a source of authority.

Validate fingerprints, closed native error classes, 4xx/5xx status codes,
nonnegative counters, timestamp relationships, ordering, page bounds and cursor
progress. Allow different counter values and first-seen outside the summary
window without reconciliation. Preserve unchanged safe selectors; redact then
bound route labels and strip query/fragment, exposing route_modified and text
truncation. Different projected labels can collide. Unsafe identifiers fail
instead of becoming modified identities. Preserve native errors without wider
queries, operator-on-behalf-of arguments, sample reads or extra permissions.
Extend performance guidance; prompt retrieval fetches no error evidence.

## Consequences

Agents can identify recurring error classes and explicitly inspect public
requests without exposing error prose or claiming that cumulative counters are
window failure rates. There are 42 read/planning tools, 50 with writes enabled
and six prompts. Protocol and pinned real Codex fixtures cover consent,
pagination context, empty terminal pages, independent error/debug retention,
ID-only follow-up, privacy and zero model requests/native writes. Portable fixture
acceptance does not qualify native/provider deployments.
