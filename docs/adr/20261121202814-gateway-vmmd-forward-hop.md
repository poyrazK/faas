# ADR-20261121202814 · A light forward hop from gatewayd-internal to vmmd

- **Status:** proposed
- **Date:** 2026-11-21
- **Amends:** ADR-052 (gatewayd-internal → vmmd is gRPC over mTLS), for
  `ForwardHTTPStream` only and only while the switches below are on.
- **Related:** ADR-20261110081523 (the edge → gatewayd-internal binary hop,
  the same idea one hop earlier), ADR-20261030230157 (vmmd local data
  socket), ADR-015 / ADR-052 (caller identity), ADR-126 (stream bridge).

This record has a date-and-time identifier because numbered ADRs collide when
parallel changes pick the next number.

## Context

A prod load test on 2026-11-21 (hello-go app, steady 1000 requests per
second on bhs-2/bhs-3, perf profiles of every daemon on the path) measured
the platform at about 5.9 ms of host CPU per request after the Go runtime
tuning of #7775. The guest app used about 0.4 ms. Per request:

| Daemon | CPU | What dominates |
|---|---|---|
| gatewayd-internal | ~2.5 ms | the `ForwardHTTPStream` call to vmmd: gRPC client stream, its HTTP/2 transport goroutines and TLS writes, ~30% of the daemon |
| vmmd | ~1.0 ms | gRPC server transport, hpack, then a second HTTP/2 client to the stream bridge |
| gatewayd-public | ~1.2 ms | client TLS and HTTP/2 |
| stream bridge | ~0.5 ms | h2c server, HTTP/1.1 to the guest |

The gRPC call carries one HTTP exchange as a bidirectional stream: an init
message (`ForwardHTTPRequestInit`: instance, method, URI, headers, sealed
edge-rule headers), body chunks, then a response init and body chunks. gRPC
adds a second framing layer on top of HTTP/2: stream setup, flow-control
windows, hpack, per-stream goroutines on both ends. None of that buys
anything here: every stream is one exchange between two fixed daemons.

## Decision

Carry `ForwardHTTPStream` over a light framed connection instead of gRPC,
in phases. Each phase ships behind switches that are off by default and is
measured on prod before the next.

### Phase 1 (this change): same messages, light transport

1. **Same port, chosen by ALPN.** vmmd's TCP listener peeks at each
   connection's TLS ClientHello without consuming it. A connection that
   offers only the ALPN protocol `gregale-vmmd-hop/1` is served by the hop.
   Every other connection is replayed, byte for byte, to the gRPC server,
   which does its own TLS handshake exactly as today. No firewall, port or
   certificate change.
2. **Same messages.** Each frame carries the protobuf message the gRPC call
   would have carried (`ForwardHTTPStreamRequest` / `ForwardHTTPStreamResponse`),
   marshaled the same way. A request starts with a preamble naming the full
   method and the gRPC metadata the client would have sent (private-forward
   flag, request correlation, W3C trace context). The response ends with a
   `google.rpc.Status`, so the client returns the same `status` error,
   details included, that gRPC would.
3. **Same handler, same interceptor.** vmmd serves the stream through the
   generated `ForwardHTTPStream` handler, wrapped by the same
   `CallerPolicyStreamInterceptor` the gRPC server uses, with a context that
   carries the connection's verified TLS state as `credentials.TLSInfo`.
   The gateway calls it through the same `VmmdClient` interface:
   `fwdStreamOnceWithEvents`, restart hold and error classification do not
   change.
4. **One exchange per connection at a time**, and a per-node pool of idle
   connections on the gateway, as the edge's binary hop does. A connection
   goes back to the pool only after a clean exchange: the client ended its
   send side and the server sent the final status. Anything else closes it.
5. **Cancellation.** The gateway cancels by closing the connection. vmmd
   reads the connection for as long as the handler runs, and cancels the
   handler's context when it ends early.
6. **Fallback.** If the TLS handshake does not negotiate the hop protocol
   (a vmmd without the switch, or before this release), the gateway uses
   gRPC for that node and retries the hop after a minute. Fallback happens
   before any request byte is sent, so it never duplicates a request.
7. **Switches.** vmmd: `FAAS_VMMD_FORWARD_HOP=1` installs the ALPN demux.
   gatewayd-internal: `FAAS_GATEWAY_VMMD_FORWARD_HOP=1` makes it offer the
   hop. Both default off. With vmmd's off nothing on its listener changes.
8. **Metrics.** `gateway_vmmd_forward_hop_total{result}` (`hop`, `fallback`,
   `dial_error`) on the gateway; `vmmd_forward_hop_conns_total{result}` on
   vmmd.

### Phase 2: binary HTTP to the stream bridge

vmmd talks to the bridge in binary HTTP (RFC 9292, `pkg/binhttp`) on its
unix socket instead of h2c. The bridge decodes it with the shared decoder.

### Phase 3: vmmd reads only the request head

The hop carries the request as binary HTTP instead of protobuf body chunks.
vmmd decodes only the head (instance, headers, sealed edge-rule headers),
does all of today's per-request work, then relays body and response chunks
to and from the bridge without rebuilding the request.

Phases 2 and 3 get their own amendment of this record before they ship.

## Security requirements

The hop must not weaken anything the gRPC path enforces. Each requirement has
a test that runs before the switches can be turned on.

1. **Same transport security.** The hop is TLS 1.3 with the same server
   certificate and the same client-certificate verification
   (`RequireAndVerifyClientCert`, the node/control-plane/service verifier)
   as the gRPC listener; the gateway pins the vmmd leaf to the node's name
   (`wire.ExpectPeerCN`) as its gRPC dial does. No plaintext path is added.
2. **Same caller policy.** Every exchange is authorized by
   `CallerPolicyStreamInterceptor` with `credentials.TLSInfo` in the peer.
   The context never carries a peer without TLS state, so the unix-socket
   trust branch of `authorizeCaller` cannot be reached. Only
   `ForwardHTTPStream` is served; any other method name is refused before a
   handler runs. Tests: a gateway certificate is served; schedd's is allowed
   as the policy allows it; builderd's and imaged's are refused; a connection
   without a client certificate fails the handshake.
3. **Same limits, strict framing.** A message is at most 4 MiB (gRPC's
   default receive limit on both ends); the preamble's metadata is at most
   64 KiB; an unknown frame type, a bad length or a malformed message closes
   the connection. The ClientHello peek and the hop handshake have a
   10 second deadline, and an idle pooled connection ends after 2 minutes.
   The frame decoder is fuzzed.
4. **Same headers and secrets.** The same handler runs, so identity-header
   stripping, sealed edge-rule header opening and the body limit are the
   same code. The gRPC path itself is unchanged: a connection that does not
   offer the hop protocol reaches gRPC as the same bytes, and gRPC's
   handshake still fills `peer.AuthInfo`.

## Consequences

- Phase 1 removes gRPC's HTTP/2 transport, hpack and per-stream goroutines
  from both daemons on the request path, keeping protobuf, TLS and every
  check. Its saving is measured on prod before phase 2.
- `ForwardRawStream`, TCP and UDP forwards, the local data socket and every
  other RPC stay on gRPC.
- A second code path serves `ForwardHTTPStream` until gRPC's is retired; the
  shared handler keeps their behavior from drifting.
