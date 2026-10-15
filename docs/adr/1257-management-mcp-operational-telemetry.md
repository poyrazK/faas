# ADR-1257: Private aggregate management MCP telemetry

## Status

Accepted. Extends ADR-1254 through ADR-1256.

## Decision

Add an optional process-owned Prometheus registry for management MCP operations.
Embedders inject a shared `Telemetry` into local or HTTP options; fresh and
restored OAuth grant servers inherit the same registry. No global registration,
Go/process collectors, per-grant collectors or durable metric state are added.

Instrument completed tools, resource reads, prompt retrieval, argument completion,
listen RPCs and subscription snapshots. Counters use only fixed operation names,
registered tool names (or `none`/`unknown`) and outcomes: `success`, `error`, `busy`,
`cancelled`, `timeout`. Histograms share fixed operation/tool labels. Classify tool
errors as failures even when the RPC succeeded; retain sanitized RPC error text
while preserving its internal cause for classification. No arbitrary error codes,
arguments, resource URIs, app/environment/account/grant IDs, credential values,
upstream messages or response bodies become labels or metric values.

Track aggregate reserved short-request/watch capacity and listen/resource slots.
Reservations increment after admission and decrement during cleanup. Count URI
invalidations only after successful per-recipient send; this is transport success,
not acknowledgement that a client reread or processed the resource. Subscription
snapshots include baselines, polls and recipient revalidation, including busy skips.
Listen outcomes describe the SDK RPC result, not a durable stream termination cause.

`gregale mcp serve --metrics-listen IP:PORT` enables the registry and a separate
loopback-only HTTP `GET /metrics` listener for either MCP transport. Require a
literal loopback IP; do not resolve hostnames or accept wildcard/public binds.
Metrics are disabled by default. The listener uses bounded HTTP timeouts, writes
startup diagnostics to stderr and closes when MCP serving exits. It is separate
from the OAuth resource and is intended for a local operator scrape.

## Consequences

Operators can diagnose contention and slow requests without retaining customer
payloads. Metrics aggregate all grants in one process, reset on restart and do not
provide per-customer accounting, traces or an event log. Handler duration includes
admission, validation and customer API reads; completed listen durations include
the stream lifetime. Gauges include reservations during setup/cleanup, not only
acknowledged subscriptions. Existing authorization/mutation audit logs remain
separate and MCP stdout remains reserved for protocol messages.
