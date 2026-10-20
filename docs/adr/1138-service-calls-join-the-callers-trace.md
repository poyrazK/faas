# ADR-1138: Service calls join the caller's trace when it serves one request

- Status: Accepted
- Date: 2026-10-19
- Related: ADR-168, ADR-169, ADR-431, ADR-20261017233500

## Context

`gregale trace <trace-id>` joins one trace's retained requests across an
account's apps and shows how each hop was woken. A service call joins the
caller's trace only when the caller's code forwards the `traceparent` of the
request it is handling. Tracing is off by default and many runtimes forward
nothing, so most service calls start a trace of their own and a request that
crossed three services shows up as three unrelated traces.

The node-local service proxy knows which instance made a call: it resolves the
caller from the source address (ADR-169). vmmd on that node forwards every
request into the instance, from any gateway, and already counts the forwards
in flight per instance to decide when a guest is idle. When that count is one,
a service call from the instance belongs to the one request it is serving,
unless the call comes from work the app does outside any request.

## Decision

**Linking.** When a guest's service call reaches the node's service proxy
without a valid `traceparent`, the proxy asks the local vmmd, by the call's
source address, for the trace context of the request the calling instance is
serving (`InflightRequestTrace`). vmmd resolves the caller from the same
host-owned live identity as `ResolveServiceCaller`, never a cached address
mapping, so a reused address cannot hand one tenant's trace context to
another; App and Job tasks serve no requests and never match. vmmd answers
only when the instance has exactly one forward in flight of any kind (HTTP,
stream, TCP, UDP, dispatch) and that forward is an HTTP request that carried a
valid W3C `traceparent`; otherwise it answers nothing and the call starts its
own trace as before. The call then continues that trace exactly as if the
caller had forwarded the header. A `traceparent` the caller sends always wins.
The answer stays inside the instance's own account: it is the trace context
of a request to the calling instance.

vmmd keeps only the `traceparent` of each HTTP forward while it is in flight,
next to the existing activity counter, and nothing else of the request. The
proxy waits at most `ServiceTraceInferenceTimeout` (25 ms, `pkg/api/limits.go`)
for the answer; a timeout, an error or an older vmmd that does not implement
the call means no link.

**Marking.** A linked call is recorded as inferred wherever it is shown. The
hop's request-telemetry row stores `trace_inferred = true` (a new column on
`request_telemetry`, carried on the gateway-to-apid telemetry wire), the
debugger and trace APIs return `trace_inferred` on the request, `gregale
trace` labels the hop "linked by Gregale", and the gateway span carries
`gregale.trace.inferred=true`. Consumers that need propagated causality, such
as the management MCP trace view (which already treats a shared trace id as
correlation only), can leave inferred hops out.

**Rollout.** The column and the telemetry field ship first, always false. The
vmmd call and the proxy change ship together in the edge/node step; an older
vmmd answers `Unimplemented` and an older gateway never asks, so either order
of node units is safe.

## Consequences

- Low-concurrency services, the common case for scale-to-zero apps, show a
  request's whole path across services with no code or configuration change.
  A busy instance serving several requests at once stays unlinked: Gregale
  does not guess between them.
- A call made by background work (for example after the response was sent)
  while exactly one other request is in flight is linked to that request. The
  `trace_inferred` mark exists so this can be told apart and discounted.
- Linking changes only trace parentage and the trace id on telemetry; routing,
  authorization, admission and billing are untouched. A target app that
  exports its own spans now receives the caller's trace context for linked
  calls, as it would if the caller had forwarded it.
- Each guest service call without trace context costs one local vmmd call
  (sub-millisecond, bounded at 25 ms) and no database query.

## Rejected alternatives

- **Using the gateway's own in-flight view.** A gateway only knows the
  requests it forwarded; several gateways can forward to one instance, so its
  "exactly one" can be wrong. vmmd sees every forward into the instance.
- **Linking busy instances by timing.** Picking the most recent or longest
  running request when several are in flight guesses; a wrong parent is worse
  than none.
- **Recording inferred links like propagated ones.** Cheaper (no column), but
  it would make a shared trace id stop meaning propagated context for every
  consumer.
- **Requiring runtime instrumentation.** Already possible (tracing on); it
  leaves every runtime without instrumentation, and every app without tracing,
  unlinked.
