# ADR-20261017232000: Incident evidence bundles through management MCP

## Status

Accepted — 2026-10-17.

## Context

The native CLI exposes `debug bundle`, while management MCP agents must assemble
request evidence, observed coverage, visible regressions and deployment comparison
through separate tools. The native bundle DTOs do not replace the narrower MCP
projections or their consent and identity checks. A combined record must preserve
independent windows and evidence references rather than imply an atomic snapshot.

## Decision

Add read-only `gregale_get_incident_bundle` for an explicit app and exact request
identifier. Validate the entire selection before native diagnostic reads. Pin the app ID
under existing app-wide diagnostic consent, reuse the guarded safe MCP projections,
and recheck the pinned app before each section. Request evidence is required; if
its read, projection or final guard fails, return a tool error without a bundle.
Do not collect app aggregates when the required evidence read fails.

Include coverage and visible regressions. Optionally compare two explicitly
supplied, distinct nonzero deployment UUIDs belonging to the app; never infer a
pair from release order or request context. An optional exact route filters only
the comparison. The optional deployment fields use diagnostic authorization,
independent of ordinary environment inspection grants. Retain deployment ownership
and scope guards. No native API endpoint, migration or SDK generation is needed.

Default aggregate `since` to one hour for all sections. Preserve each result's own
native effective window, clamping, counts, source limitations and truncation. This
lookback does not filter the exact request; visible regressions can concern other
requests/routes/deployments. Independent sequential reads are not atomic. Envelope
schema `gregale.mcp.incident/v1` records source tools and bridge start/finish times
per attempted section; native generated/detection times remain inside results.
JSON Pointer references inside evidence remain relative to that section's result.

Limit regression observations and comparison routes to five each using existing
native-order output projections without pagination. Limit safe serialized request
evidence to 48 KiB and every optional result to 16 KiB, within the shared 128 KiB
output bound. Reject an oversized section with `response_too_large`, preserving
its source-tool name for separate inspection. Do not trim evidence arrays or
rewrite their references to make a bundle fit. All limits live in pkg/api/limits.go.

Bound each read and final guard by five seconds and a share of the remaining tool
deadline, reserving time for final checks and an app identity check. Optional
failures produce code/status/nonnegative retry-delay metadata without native
problem prose or diagnostic data. Comparison is `not_requested` when omitted;
requested sections are `ok` or `error`. Bundle status is `ok` or `partial`; partial
means optional read/check failures, not incomplete capture or health. Cancellation,
overall timeout, consent loss or root app identity change fails the whole call.
Immediately before serialization recheck every successful section. A failed
optional guard discards that section's result; a failed required guard withholds
the bundle. Recheck the pinned root app after all section guards.

## Consequences

Agents receive one bounded investigation record suitable for returning to their
user for review, with no automatic upload, file write, sharing, follow-up paging,
collection, VM wake, triage or native mutation. The catalog advances to 48
read/planning tools or 57 with existing writes. Portable fixtures and real Codex
MCP-client checks exercise reads without model requests or native writes; they do
not qualify native/provider deployment behavior.

Adversarial review covered broad environment authorization through optional IDs,
slug replacement between reads, late deployment ownership/scope changes,
per-section and overall cancellation, large escaped JSON strings, accidental
request filtering by aggregate windows, and nested evidence-pointer semantics.
