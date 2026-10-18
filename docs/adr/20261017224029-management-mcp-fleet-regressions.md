# ADR-20261017224029: Selected-app regression inbox through management MCP

## Status

Accepted — 2026-10-17.

## Context

The native CLI composes app-scoped regression reads for a fleet overview.
Management MCP currently requires one tool call per app. Agents need a bounded
overview that preserves diagnostic consent, app identities and native evidence
limits, including when only some selected apps can be inspected.

## Decision

Add the read-only `gregale_list_fleet_regressions` tool. Require an explicit list
of one to five distinct app slugs. Do not discover or include future apps.
Validate the entire selection and lookback before making API requests. For each
selected app, reuse the existing guarded regression read and safe projection,
including separate diagnostic consent, immutable app/deployment ownership and
scope checks. Preserve selection order and native per-app observation order.
Do not combine metrics, infer a cross-app ranking or report fleet health.

Accept one requested detection lookback (native default one hour). Each successful
app retains its own native effective lookback, retention clamping, counts,
visibility, timestamps and unknown completeness. `limit_per_app` defaults to five
and is capped at ten. Each safe per-app result is additionally capped at 16 KiB
by trimming its native prefix and explicitly setting response-size and regression
truncation flags; no pagination or additional reads are implied. These bounds
live in pkg/api/limits.go and preserve the shared MCP response/frame limits.

Read apps sequentially. Bound each initial read and final guard by five seconds
and a fair share of the tool deadline, reserving half the request budget for
final checks. A per-app error becomes a code/status/retry-delay entry without
native error prose or diagnostic data. Return all requested app entries even
when every read fails. The envelope distinguishes ok, partial and unavailable;
partial indicates read failures, not evidence completeness or recovery.
Global cancellation/deadline expiry fails the whole call.

Immediately before serialization, recheck every successful app's guarded
identities under the current authority. Discard an app's collected result if its
guard fails, preserve other valid results, and recompute result counts/status.
Independent app reads and checks are not an atomic snapshot. Triage continues
through the existing separately reviewed plan/apply tools. No background watch,
subscription, mutation, diagnostic collection or VM wake is started.

## Consequences

No native API endpoint, database migration or generated native SDK changes are
needed. MCP discovery and CLI tool counts advance by one. Tests cover selection
validation, denied apps without diagnostic reads, per-app errors/timeouts,
cross-app ownership, identity changes during later reads, bounded projections,
redaction, cancellation, both protocol versions and real-client partial results.
Portable fixtures do not qualify a native/provider deployment.
