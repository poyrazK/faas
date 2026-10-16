# ADR-1257: MCP transport observability and retained diagnostics

## Context

Compatibility and smoke checks cannot explain production traffic failures. The
gateway already propagates request IDs and trace context, publishes retained
request telemetry, and exposes request evidence under account-scoped access.

## Decision

Add gateway counters and duration histograms for requests carrying MCP method or
protocol-version headers. RPC method labels use a closed vocabulary; unknown
methods collapse to other. They are caller-claimed hints, not parsed or verified
body fields. No tool names, arguments, tokens or result contents become telemetry.
Requests without MCP headers are excluded, including legacy clients that omit them.
Known-app attribution reuses resolved routing identity. Route probes are excluded.

Transport outcomes distinguish authentication, invalid requests, rate limits,
timeouts, cancellation and upstream errors. HTTP success does not establish
JSON-RPC success. A streaming-forwarder gauge balances on completion and panic;
it includes JSON responses forwarded on that path, rather than claiming to count
only open SSE connections. Aborted forwarding records interrupted_stream and
rethrows the original panic. Failure logs correlate existing public request IDs
and distributed trace IDs without including raw errors or payloads.

`gregale mcp diagnostics --app` reads one bounded page from the existing retained
request API, filtered by the configured route label. It weights aggregate rows by
count, reports partial pagination and retention clamps, groups HTTP outcomes, and
links each retained failure to the existing request evidence endpoint. It reuses
plan, scope, MFA and ownership gates; no new data store or server endpoint is added.
Retained diagnostics do not carry RPC method hints or identify all post-header
stream interruptions. Operator metrics/logs provide those additive signals.

## Consequences

Operators can inspect method-level request counts, latency, active forwarding
streams and transport failures. Customers can correlate retained failures with
execution evidence. This is transport evidence; successful HTTP responses may
still contain RPC errors. Route labels may be manifest patterns rather than literal
endpoint paths. Missing or expired telemetry is not proof of healthy operation.
No VM lifecycle or billing behavior changes apply.
