# ADR-20261102082606: Service calls inherit the caller's deadline

- Status: proposed
- Date: 2026-11-02
- Related: ADR-093 (request budgets), ADR-1138 (service calls join the
  caller's trace), ADR-1150 (`X-Faas-Deadline-Ms`), ADR-570 (`reqbudget`
  started budgets), ADR-20261023115741 (switches before release)

## Context

The gateway tells a guest how long its request may still run in
`X-Faas-Deadline-Ms` (ADR-1150). When that guest calls another app through
`<slug>.svc.gregale`, the node's service proxy bounds the call only by the
binding's `service_reliability.timeout_ms` (or nothing). It does not know the
caller's own deadline. So when a public request times out at the edge, the
downstream services it called keep working on an answer nobody will read:
they wake, hold instances and run queries for a dead request. A chain of
three services can spend three times the work of the request that already
failed.

Kubernetes users get this from gRPC deadline propagation or a service mesh,
and only when every service forwards the deadline. Gregale already knows
both ends of every hop:

- vmmd tracks every HTTP forward to an instance while it is in flight, for
  trace linking (ADR-1138), and sees the `X-Faas-Deadline-Ms` the gateway
  sent with it.
- The service proxy already asks the local vmmd which request the calling
  instance is serving (`InflightRequestTrace`), and resolves the caller from
  host-owned identity, never a guest header.

## Decision

### 1. vmmd records each forward's deadline

When vmmd starts an HTTP forward it records the absolute deadline from the
forward's `X-Faas-Deadline-Ms` (receipt time plus the value) beside the
traceparent it already keeps. `InflightRequestTraceResponse` gains
`deadline_unix_ms` (0 when unknown). vmmd answers it under the same rule as
the traceparent: only when the instance has exactly one forward of any kind
in flight, so a call is never bound by another request's deadline. Unlike the
traceparent, the deadline is answered even when that request carried no
trace context.

### 2. The service proxy bounds the call by the caller's remaining time

After authorization and the binding's reliability timeout, the proxy works
out the caller's deadline:

- from `X-Faas-Deadline-Ms` on the call, when the caller's code forwarded it
  (the SDKs and runners may), or
- from vmmd, for a caller that forwarded nothing.

It then bounds the call with `reqbudget.WithStarted`, using the remaining
time minus `api.ServiceCallDeadlineHopOverhead` (10 ms) as the total.
`WithStarted` only ever tightens: a caller-supplied value shortens the call
and can never extend it past the binding's timeout or the proxy's own
budget. The target guest then receives the shortened `X-Faas-Deadline-Ms`
through the existing forwarder, which drops any customer-supplied `x-faas-*`
header first, so a chain of hops keeps shrinking the deadline.

When the caller has no time left, the proxy answers the existing `504
request_budget_exceeded` before forwarding, so the target is not woken for a
request nobody will read. It is not counted against the target app's own
budget.

A call from an instance serving several requests at once, from an app or job
task, or from an older vmmd (which leaves the field 0) keeps today's
behaviour.

### 3. Behind an operator switch

`FAAS_SERVICE_CALL_DEADLINES=1` on gatewayd-internal turns it on (default
off, `pkg/daemonunitspec/envcontract.go`). With the switch on, the proxy asks
vmmd on every service call that did not forward a deadline, not only on
calls without a traceparent. The lookup is a local unix-socket RPC bounded by
`api.ServiceTraceInferenceTimeout` (25 ms), and it is one call for both
answers. The switch stays off until a production A/B shows the added service
call latency is negligible. No CP Postgres call is added to the request path.

## Consequences

- A downstream service stops work for a request that has already timed out,
  and does not wake for one, without any customer code change.
- Handlers can read the remaining time in `X-Faas-Deadline-Ms` on internal
  calls as they already can on public ones. The function runners keep their
  30 s floor (ADR-1150): the proxy cancels the call when the deadline passes,
  whatever the runner's own bound.
- The guess is conservative: it applies only when the calling instance serves
  exactly one request. Concurrent instances need the caller to forward the
  header, which the SDKs can do in a later step.
- Release compatibility: the proto field is new and optional. An old vmmd
  answers 0 and an old gatewayd-internal ignores the field, so release N runs
  beside N-1 in either order.

## Steps

1. This ADR, the vmmd field, the proxy bound and the switch (one PR).
2. Measure added service call latency on production with the switch on one
   node; then default it on.
3. SDK and runner helpers that forward `X-Faas-Deadline-Ms` on service calls,
   which extends the bound to concurrent instances (amendment 1: the SDKs;
   amendment 2: the Node and Python function adapters).

## Amendment 1 (2026-11-04): step 3 in the SDKs

- The SDKs' existing request-context helpers carry it, so apps already
  forwarding the release get it without new wiring: Go
  `GregaleReleaseMiddleware` + `NewGregaleReleaseTransport`, Node
  `withGregaleRequestContext` + `createGregaleFetch`, Python
  `GregaleReleaseMiddleware` / `with_gregale_request_context` + the HTTPX
  transports.
- Inbound, the helper reads one valid `X-Faas-Deadline-Ms` (a positive whole
  number up to a day; otherwise nothing) and keeps the absolute time it runs
  out. It never cancels the handler's own context.
- Outbound, only on the managed service hosts each SDK already forwards the
  release to (`*.svc.gregale` and declared `*.internal` aliases; Go added
  `*.internal` on 2026-11-05 with the signed-carrier parity fix), it sets the header to the time left, keeping a smaller valid value the
  caller set itself. With no time left it sends 1 ms, so the proxy answers 504
  `request_budget_exceeded` without waking the target, as it would for an
  inferred deadline. External hosts get nothing added.
- Handlers can read the time left: Go `GregaleDeadlineFromContext`, Node
  `gregaleRequestTimeRemainingMs()`, Python
  `gregale_request_time_remaining_ms()`.
- The function runners are unchanged: they do not run handlers inside the
  SDK's request context. A function that calls services wraps its handler in
  the helper as an app does. (Superseded for Node and Python functions by
  amendment 2.)
- Nothing changes until `FAAS_SERVICE_CALL_DEADLINES` is on: with it off the
  proxy ignores the header and its forwarder drops customer `x-faas-*`
  headers.

## Amendment 2 (2026-11-05): function adapters enter the SDK's context

A function's handler is called by the adapter Gregale writes into its image
(`pkg/rootfs`), not by customer code that could wrap it, so step 3 reached
functions only when the customer wrapped the handler by hand. The adapters
now do it:

- The Node SDK registers `withGregaleRequestContext` under
  `Symbol.for("gregale.sdk.requestContext.v1")` on `globalThis` when it
  loads; the first SDK copy in a process registers, a second one does not
  replace it. The Node adapter looks the symbol up on each call and, when it
  is a function, runs the handler and the response it returns, streamed
  bodies included, inside it.
- The Python adapter looks up `faas_sdk.release_context` in `sys.modules` on
  each call and, when it is loaded, enters `with_gregale_request_context`
  with the request's headers around the handler and its body: on the
  sequential worker, on an async worker's task, and on a sync handler's
  thread. Coroutines that a handler thread hands to the event loop re-enter
  the context in the loop, since `run_coroutine_threadsafe` does not carry it.
- The adapters never import the SDK. A function that does not load it runs
  exactly as before and pays nothing. The context carries the release, the
  signed chain deadline (ADR-570) and the time left, so all three follow a
  function's service calls.
- Go functions are unchanged: `function.Serve` hands the handler an
  `*http.Request` with the request headers, so wrapping it in
  `faas.GregaleReleaseMiddleware` already works. The `function` package stays
  free of the full SDK so function binaries stay small.
- Release compatibility: the adapter is written into the image at build time,
  so a function picks this up on its next build. An older SDK does not
  register the symbol and the adapter falls back to calling the handler
  directly.

## Alternatives considered

- **Require customers to forward the header.** Most code never will; the
  benefit would reach only the apps that need it least.
- **Track deadlines in the gateway instead of vmmd.** The gateway does not
  know which instance a guest-originated call comes from without vmmd's
  host-owned identity; vmmd already holds both.
- **Bound by the caller's deadline even when the instance serves several
  requests (use the latest one).** A long request would then be cut short by
  a short one beside it. Refused.
