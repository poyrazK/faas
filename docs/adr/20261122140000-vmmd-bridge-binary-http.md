# ADR-20261122140000 · vmmd sends HTTP/1.1 apps' requests to the stream bridge in binary HTTP

- **Status:** proposed
- **Date:** 2026-11-22
- **Amends:** ADR-126 and ADR-127 (vmmd → stream bridge is h2c), for HTTP/1.1
  apps only and only while `bridge_binary_http` is on.
- **Related:** ADR-20261110081523 (the edge → gatewayd-internal binary hop,
  whose client and server this reuses), ADR-20261121202814 (the gateway →
  vmmd hop, parked: prod forwards no longer take that leg),
  ADR-20261026120000 (bridge stream headroom).

This record has a date-and-time identifier because numbered ADRs collide when
parallel changes pick the next number.

## Context

A prod profile on 2026-11-22 (hello-go app, steady load to 1000 requests per
second, bhs-2 perf for 30 s) measured about 6.4 ms of platform CPU per
request. Forwards now reach vmmd over its local unix data socket, so the
gateway → vmmd leg carries no TLS and little transport cost. The vmmd →
stream bridge leg still runs HTTP/2 over a unix socket: vmmd's x/net/http2
client (request header encoding, the connection's read loop) and the
bridge's h2c server (frame reading, hpack decoding, its write scheduler).
Together they cost about 0.4 ms per request.

`BenchmarkInternalHop` (one CPU, unix socket, the production
implementations) puts binary HTTP at about a third of h2c's cost: 7.7–10.8
against 22.4–29.4 µs per exchange. `BenchmarkBridgeFraming`, which runs
vmmd's side, the real bridge and an HTTP/1.1 guest in one process, measured
64–68 µs of process CPU per exchange over binary HTTP against 90–151 µs over
h2c. Scaled as the edge hop's prod profile scaled against its benchmark,
the saving is about 0.15–0.25 ms per request, 2–4% of platform CPU.

## Decision

1. **Binary HTTP on the same socket.** A bridge vmmd starts for an HTTP/1.1
   app with `bridge_binary_http` on (`FAAS_VMMD_BRIDGE_BINARY_HTTP`) gets
   `FAAS_BRIDGE_BINARY_HTTP=1`. It then serves binary HTTP (RFC 9292,
   `pkg/binhop`) beside h2c on its one socket. A connection's first byte
   tells them apart: binary HTTP starts with `0x00` or `0x02`, which no
   HTTP/1.1 method, the HTTP/2 preface or a session hello does. Sessions
   are split off first, as before.
2. **The same handler.** The binary HTTP server decodes each request into an
   `*http.Request` and serves the bridge's own handler, the one h2c wraps:
   one guest pool, one completion state, the same header stripping and the
   same net/http guest transport, which frames the request the guest sees.
3. **The same request at the guest.** vmmd's binary HTTP client adds what
   its x/net/http2 client added: `User-Agent: Go-http-client/2.0` when the
   request has none, and `Accept-Encoding: gzip` (decoding the gzip it asked
   for) as net/http does. `TestBinaryHTTPReachesGuestAsH2C` sends the same
   requests both ways and requires the guest to see identical requests.
4. **Scope.** HTTP/1.1 apps only. HTTP/2 and gRPC apps need full-duplex
   HTTP/2 to their guest and keep h2c, as do raw, TCP and UDP sessions and
   the per-RPC legacy bridge.
5. **Rollout.** The switch defaults off and applies to bridges vmmd starts
   after it is on. A bridge adopted from a predecessor keeps h2c: the
   adoption record carries no framing, and adding a field would make a
   rolled-back vmmd refuse the journal (it decodes strictly). Every bridge
   still serves h2c, so turning the switch off or rolling back needs
   nothing else.

## Security requirements

The change must not weaken what the h2c leg enforces. Each item has a test.

1. **Who can connect.** Unchanged: the same 0600 socket under the 0700
   stream directory, and vmmd's binary HTTP dial runs the same
   `verifyBridgePeer` check (the instance's bridge uid) as its h2c dial.
2. **What root vmmd parses.** vmmd now decodes the bridge's responses, which
   carry guest-controlled fields, with `pkg/binhttp`/`pkg/binhop` instead of
   x/net/http2. The decoder enforces net/http's field rules (token names,
   no CR, LF or NUL in values) and a 1 MiB head and trailer budget, the h2c
   client's header list cap. `FuzzReadResponse` and `FuzzTransportResponse`
   fuzz it, the latter as a compromised bridge would answer. Gzip is decoded
   as a stream, as the h2c client did.
3. **Framing cannot be read two ways.** A message's content must match its
   `Content-Length` exactly; malformed or disagreeing values are refused on
   requests and responses; a client reads at most five informational
   responses. The guest request is still built by net/http's transport,
   which ignores `Content-Length` and `Transfer-Encoding` fields and frames
   the body itself. Tests: `TestTransportHoldsResponseToContentLength`,
   `TestTransportRefusesConflictingContentLength`,
   `TestTransportBoundsInformationalResponses`,
   `TestServerHoldsRequestToContentLength`, and the conflicting-length and
   `Transfer-Encoding` cases of `TestBinaryHTTPReachesGuestAsH2C`.
4. **Private and hop-by-hop fields.** `X-Faas-Bridge-*` fields, hop-by-hop
   fields and, now for either framing, the fields a `Connection` field lists
   (RFC 9110 §7.6.1) never reach the guest. x/net/http2's client refused a
   request with such a `Connection` field; binary HTTP carries it, so the
   bridge strips what it lists.
5. **Limits.** The binary HTTP server takes the h2c server's: 1 MiB header
   size, a 10 s header read timeout as the slowloris defence, a 120 s idle
   timeout, and no read or write timeout, so uploads and streamed responses
   run as long as on h2c. vmmd keeps at most 64 idle connections per bridge;
   in-flight requests are bounded by vmmd's admission, as with h2c.
6. **A small bridge.** The client and server moved from `pkg/gateway` into
   `pkg/binhop`, which depends only on `pkg/api` and `pkg/binhttp`; the
   bridge links two more of the repo's packages, not the gateway.

## Consequences

- About 0.15–0.25 ms of CPU less per HTTP/1.1 request, to be confirmed on
  one prod node (both nodes' vmmd with the switch, A/B against the switch
  off) before the default changes.
- The edge → gatewayd-internal hop also gains the `Content-Length` and 1xx
  checks, which net/http's HTTP/1.1 hop already made.
- Two framings reach the bridge handler until h2c is retired for HTTP/1.1
  apps; the shared handler and the parity test keep them from drifting.
