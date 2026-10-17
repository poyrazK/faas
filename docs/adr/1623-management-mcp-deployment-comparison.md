# ADR-1623: Retained deployment comparison through management MCP

## Status

Accepted. Extends ADR-1618–1622 and the native ADR-127 comparison API.

## Decision

Add read-only `gregale_compare_deployments` using the existing native
`CompareAppDebugDeployments` client. Accept an app, two exact distinct nonzero
deployment UUIDs, optional exact route/lookback/window end, and an output route
limit (default 20, maximum 50). The native body retains its `source`/`mirror`
vocabulary; the tool exposes `source_deployment_id`/`target_deployment_id`.
These are comparison labels and do not imply release order or mirror execution.
The endpoint's POST performs only telemetry reads. No replay or mutation is
introduced. Output limits are not sent to the native query.

Reuse app-wide diagnostic consent, pinned to the reviewed app ID. Validate both
deployments belong to that app and pin their scopes before comparison. Recheck
each ownership/scope after final shared authorization, then app consent/identity
last before output. A narrow typed authorization branch handles comparison IDs
under diagnostic consent; ordinary deployment/environment tools keep their
existing permissions. Local stdio uses native credential authority. No log,
environment or write permission is expanded.

Extend the native `DebugCompareResponse` additively with app identity, effective
lookback, start/end, retention-clamping flag and applied exact route filter.
Fields remain optional for older-server SDK compatibility. A pointer preserves
absence versus false for retention clamping. MCP requires this metadata and
returns `comparison_metadata_required` on an older server instead of inventing
the window. Verify echoed IDs/filter, app ID, window duration, requested maximum
lookback and explicit window end. Native errors are returned without fallback,
retries, wider windows or another comparison method.

Move native comparison orchestration/union into `pkg/apid/debugcompare`, with
the existing server-owned SQL reader supplied as a callback. Capture the native
clock once and use the same window for both sequential reads. Keep the native
one-hour default, distinct from other debugger aggregates' 24-hour defaults.
Reject invalid/nonpositive/overflowing lookbacks and invalid/future window ends
instead of silently defaulting them. Clamp the start to the current plan's
absolute retention cutoff, including historical `until` selections. Reject a
window ending at/before that cutoff. Report the actual resulting duration and
clamping. App ownership, read scope, MFA and plan gating remain in the handler.
The native SQL reads remain app/deployment scoped, with no schema or migration.

Project route percentiles and represented request counts explicitly. Preserve
the route union, native ordering and missing-side semantics. A positive count
with zero latency is observed evidence; a zero count has no exported latency
percentiles and is labeled `no_retained_samples`. A row is comparable only when
both sides have samples. Do not compute significance, causal regression flags
or automatic rollback recommendations from these distributions. Different
traffic mixes and sample sizes may explain differences. Source capture/read
completeness is unknown; matching windows do not create an atomic snapshot.

Centralize transport route/output/window-end budgets in `pkg/api/limits.go`.
Preserve projection route/text truncation, redact known credentials before
256-byte text bounds, remove route query/fragment data and reject unsafe route
selectors. Exact identifiers are canonicalized UUIDs. Text remains private and
untrusted, and redaction is not a detector for every arbitrary secret.

Extend the existing performance prompt's guidance when the user supplies two
exact IDs. Keep prompt arguments/completion unchanged and fetch no evidence
during prompt retrieval. Do not silently choose latest/previous deployments.
Regenerate Node/Python clients and sync the embedded OpenAPI specification.
Python regeneration must preserve current handwritten test bytes through its
formatter passes, just as it preserves authored SDK modules. Extend the existing
source-preservation guard to test inputs and verify uncommitted edits survive.

## Consequences

Agents can assess retained release differences with explicit windows and absent
samples while preserving diagnostic consent and deployment ownership. Older
servers require an API upgrade for the new tool. The native comparison endpoint
now rejects previously silently defaulted invalid windows and windows wholly
outside current retention. No collection, replay, model call or platform write
is added. Native handler/domain, Go SDK, MCP and pinned real Codex fixtures cover
the new contracts; provider/native deployment qualification remains separate.
