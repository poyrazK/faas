# ADR-197 · Internal service wire protocols

- **Status:** accepted
- **Date:** 2026-09-21

## Context

ADR-124 gave apps a wire-protocol selector (`apps.app_protocol` ∈ {`http1`,
`http2`, `grpc`}) and ADR-126 gave vmmd the matching guest bridges: the H1
bridge for `http1`, the H2C bridge for `http2` and `grpc`. The public edge
selects between them by stamping `x-faas-protocol` on the request, which
`fwdStreamOnce` copies into `ForwardHTTPRequestInit.AppProtocol`. ADR-080
added a separate verbatim-bytes path (`ForwardRawStream`) so `Connection:
Upgrade` handshakes survive the hop instead of being stripped as hop-by-hop
headers.

The node-local service proxy (ADR-168/169/170) had none of this. Three
separate defects followed, and each one failed quietly rather than loudly:

1. **The guest hop never stamped `x-faas-protocol`.** `fwdStreamOnce`
   defaults an empty value to `http1`, so every internal call — including
   calls to an app the customer explicitly configured as `grpc` — was routed
   to the H1 guest bridge.
2. **The guest listener spoke HTTP/1.1 only.** It is built by the same
   `defaultServer` factory as the loopback control listener, so a workload's
   gRPC client could not complete H2C prior-knowledge negotiation against
   `HostBridgeIP:10080` at all. That factory also applies the control
   listener's 30 s `WriteTimeout`, which `http.Server` starts *before* the
   handler runs: it bounds the entire exchange and would truncate a streaming
   response, an upgrade session, or a call held through a snapshot restore
   (ADR-196).
3. **Trailers were dropped.** `serviceProxyResponseWriter` buffers the
   response so a 5xx from a stale target can be retried, and it snapshots the
   header map at commit time. Trailers are by definition written after the
   headers, so `grpc-status` never reached the caller — who then read a
   complete, apparently successful stream carrying no status at all. This is
   the same class of defect found at the public edge in the 2026-09-21 e2e
   coverage work.

The net effect: the standards matrix claims gRPC support for customer apps,
and that claim held at the public edge while silently failing between two
internal services — the topology where gRPC is most likely to be used.

## Decision

The service proxy selects the guest bridge from the resolved target, exactly
as the public edge does.

### Carry protocol with identity

`ServiceProxyResolver` now returns a `ServiceTarget` (app id, `AppProtocol`,
`WebSocketEnabled`) instead of a bare app id. Resolution already reads the
apps row to authorize the call, so the protocol posture rides along at no
extra cost; a separate resolver seam would have added a second store read to
the request path for data we had already fetched.

`serviceGuestProtocol` mirrors `decideProtocol` on the public path, including
its degradation rule: a value outside the column's closed set becomes `http1`
rather than failing the call.

### Upgrade traffic takes the raw bridge

`ServiceProxy.dispatch` routes `isUpgradeRequest` traffic to an optional
`RawForward` seam, wired in production to the same `nodeCache.RawForwarding()`
the public edge uses. The upgrade path is deliberately neither buffered nor
retried: the response is a hijacked connection, so the first endpoint picked
is the only one, and a stale-target signal cannot be acted on once bytes have
flowed.

Two gates fail closed with `501`, matching the public edge's posture rather
than falling through to the plain forwarder:

- the target has `websocket_enabled=false` — a customer who turned upgrades
  off does not get them back through the service mesh;
- no raw bridge is wired on this node.

Falling through instead would strip the handshake and surface a `502` that
clients retry in a loop (the ADR-080 / issue #707 failure mode).

### Guest listener speaks H2C

The guest service-proxy listener sets `Protocols.SetHTTP1(true)` +
`SetUnencryptedHTTP2(true)` (the Go 1.24+ native replacement for
`x/net/http2/h2c`) and widens its read/write deadlines to the customer request
envelope the public listener uses.

### Trailers are propagated

`commitTrailers` runs after the forward returns and copies across any key that
appeared in the buffer's header map after commit — both Go's
`http.TrailerPrefix` form for undeclared trailers and plain keys for declared
ones. Pinned by `TestServiceProxyPropagatesTrailers`, which fails without it.

## Consequences

- A workload can call `http://orders.svc.gregale:10080` with a gRPC client and
  reach an app configured `app_protocol=grpc` over the H2C guest bridge, with
  `grpc-status` intact.
- WebSocket and other `Connection: Upgrade` sessions work between same-account
  services, subject to the target's own `websocket_enabled` setting.
- The guest listener no longer truncates long-lived exchanges at 30 s.
- Plan gating is unchanged and still enforced where it already lives: `grpc`
  as an app protocol is validated by `Plan.AppProtocolAllowed` at the apid
  boundary, and upgrade traffic by the target's `websocket_enabled` column.
  This ADR adds no new entry to `pkg/api/limits.go`.
- `ServiceProxyResolver` returns `ServiceTarget`. ADR-212 later adds the caller
  app id to its inputs and `ServiceTarget.PreviewScoped` so resolution can be
  scoped to a PR environment and telemetry can distinguish the selected
  target environment.

## Rejected alternatives

- **A second resolver seam for protocol.** Another store read per request for
  data the authorization lookup already had in hand.
- **Inferring the protocol from the request** (`content-type:
  application/grpc`, `:authority`, HTTP version). The app's configured
  protocol is authoritative and already validated against a closed set;
  sniffing would disagree with the public edge for the same target.
- **Routing upgrades through the plain forwarder.** Strips Connection/Upgrade
  as hop-by-hop headers and produces a retry loop instead of a clear error.
- **Retrying upgrade requests on a stale target.** The response is a hijacked
  connection; there is no safe point at which to switch endpoints.
- **Raw TCP ingress for internal services.** Out of scope here. ADR-183
  (public raw TCP) and ADR-176/177 (named ports, host-port leasing) own that
  surface; the service mesh addresses a service by name, and a name has no
  port to disambiguate a non-HTTP listener without extending the discovery
  contract itself.
