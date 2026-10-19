# ADR-20261017233500: Explicit selected-app traces through management MCP

## Status

Accepted — 2026-10-17.

## Context

`gregale trace` reads request, log/span and durable invocation evidence across
an account. MCP grants diagnostic access separately per app. Filtering that
account-wide response would fetch unconsented evidence and allow other apps to
consume the limit. A native selected-app contract must precede MCP trace lookup.

## Decision

Add `GET /v1/account/traces/{trace_id}/selected-apps`, requiring one to five
distinct canonical nonzero app UUIDs owned by the authenticated account. Validate
all ownership before source reads and recheck app identities before returning.
Never widen an empty/invalid selection to account inventory. Keep the existing
CLI/account endpoint unchanged. New SQLC queries filter account, selected app IDs
and exact nonzero W3C trace ID before ordering/limiting. Fetch only request
metadata and invocation lifecycle columns; no logs, spans_summary, payloads,
results, queue names or arbitrary headers. The canonical trace header is only
an indexed invocation predicate. No migration or VM lifecycle change is needed.

Request rows are oldest-first by received_at/ID within current plan telemetry
retention, using UTC PostgreSQL microsecond window boundaries. Durable invocation
rows are independently oldest-first by created_at/ID, without a request-window
filter. Default ten and maximum twenty rows per source; fetch one extra sentinel
to detect truncation. Put all limits in pkg/api/limits.go. Report the request
window/retention days, selected app ID/slug roster, per-source status and truncation.
Sources are independent live reads, not an atomic snapshot or pagination cursor.
Failed sources are unavailable with empty arrays and no native error prose; both
unavailable sources still return an explicit partial result. Empty results do not
establish completeness, health or absence of an account-wide trace.

Add read-only `gregale_get_trace` for an exact trace ID and explicit app slugs.
Resolve and authorize every app under diagnostic consent before invoking the
scoped API once. Require its explicit_app_ids_v1 marker and exact selection,
window, source-status/order and limit metadata. There is no account-wide or older
API fallback, automatic app discovery or evidence enrichment. Pin request
deployment ownership/scopes and recheck every selected app and exposed deployment
before serialization. Any authority/identity failure withholds the whole result.
Source failures remain partial/unavailable evidence, not consent failures.

Project safe request lookup links and bounded invocation source/state/attempts
and lifecycle times. Shared trace ID means correlation; do not infer parent-child
edges, root cause, cross-source causal order or complete distributed capture.
started_at is the latest claim/delivery time, not the first attempt; do not derive
per-attempt duration or queue wait. Request rows can represent multiple requests.
Use existing credential redaction, text and response bounds. Application labels
are private untrusted data. No logs, raw spans/attributes, traceparent, payloads,
headers, queue names, collection, VM wake, replay, invocation cancellation,
subscriptions or writes are introduced.

## Consequences

OpenAPI and typed Go/Node/Python clients gain an additive endpoint. N-1 APIs fail
without a broader fallback. MCP advances to 49 read/planning tools or 58 with
existing writes. MemStore reports request telemetry unavailable and provides
selected durable invocation metadata. PostgreSQL evidence verifies account/app
filtering before limits, retention boundaries, equal-time ordering and sentinel
truncation. Native/domain/MCP and pinned real-client checks cover consent, invalid
selections, partial sources, redaction, late identity/scope changes and cancellation.
Portable fixtures do not qualify native/provider deployments.

Adversarial review covers omitted selectors, foreign apps, slug replacement,
pre-limit starvation by unselected rows, ignored native selection, invalid windows,
source flags/order, raw-data exclusion and lifecycle interpretation. Reading a
pending invocation never cancels, replays or otherwise changes it.
