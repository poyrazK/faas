# ADR-20261017191502: Bounded starter protocol observations

## Status

Accepted; extends ADR-1257's transport observability without changing its claims.

## Context

HTTP 200 may carry an RPC or tool error. Gateway transport metrics cannot infer
application protocol success. Node callback logs do not cover SDK-generated RPC
errors, and Go/Python lack equivalent bounded protocol observations.

## Decision

Emit one mcp_protocol_request event from parsed requests and protocol response
objects. Observe Node SDK messages through its public transport boundary and
Go/Python response helpers; never inspect serialized response bodies. Preserve
separate protocol outcome and response-stream termination fields.

Use fixed method/tool label sets. Group unknown methods and application tools
under other. Omit arguments, payloads, errors, resource identifiers, identities
and RPC IDs. Allow only UUID request correlation and valid nonzero version-00
traceparent IDs; treat them as caller-influenced correlation, not identity.

Measure dispatch-to-response construction separately from SSE response lifetime.
Subscription acknowledgment ends setup timing. Omit unobserved timing instead
of inventing completion; Task creation measures admission, not worker execution.
Distinguish observed cancellation/typed deadlines without adding execution limits.

## Consequences

Operators can aggregate structured events into bounded counters/histograms.
This does not add a public metrics endpoint or change retained HTTP diagnostics.
Pre-parse and pre-dispatch authentication failures, notifications and background
Task execution remain separate evidence. Node callback diagnostics remain, with
fixed tool labels; authorization tests count callbacks separately from protocol
observations. Native/provider release qualification remains required.
