# ADR 20261110081523 · The compute edge reaches its own gatewayd-internal over binary HTTP

- **Status:** proposed
- **Date:** 2026-11-10
- **Amends:** ADR-070 (the edge → gatewayd-internal hop is HTTP/1.1 over a
  unix socket), for a compute edge's requests to its own node only. The
  process split of ADR-070 and ADR-1343 stays as it is.
- **Related:** ADR-1343 (app edge on compute nodes),
  ADR-20261031105055 (placement routing; requests to other nodes),
  ADR-20261020160744 (daemon blast radius), issue #675 (`FAAS_INTERNAL_H2C`),
  [the 2026-11-09 edge request cost report](../ops/evidence/20261109-edge-request-cpu/REPORT.md)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

Every app request a compute node serves crosses two HTTP servers. The edge
(`gatewayd-public` in compute mode) terminates TLS and HTTP/2. It then sends
the request as HTTP/1.1 over `/run/faas/gatewayd-internal.sock`, and
gatewayd-internal parses it again before routing, waking and proxying it.

The edge load test of 2026-11-09 put each node's ceiling at about 3,500
requests per second for a request that names no app. The same two daemons
then spent about 1 ms of CPU per request. After the request-log budget
(#6868) and the final-chunk flush fix (#6869) shipped in rc.336, the same
profile on `gregale-compute-1` reads:

- 450 404s per second;
- 30 s CPU profiles of both daemons;
- 13,500 requests in the window.

| Part of the hop | CPU | Per request |
|---|---|---|
| Edge: the HTTP/1.1 client (`upstreamTransport`, read and write loops) | 0.94 s | 70 µs |
| Edge: the `otelhttp` wrapper around that client | 0.64 s | 47 µs |
| gatewayd-internal: the HTTP/1.1 server (read the request, finish the response, peek, background read, deadlines) | 1.22 s | 90 µs |

**Totals, per 404:**
- edge: 0.55 ms;
- gatewayd-internal: 0.29 ms;
- together: 0.84 ms.

**Shares of those 0.84 ms:**
- the HTTP/1.1 hop itself, 160 µs: **19%**;
- the `otelhttp` wrapper, 47 µs: 6%. Of that, 35 µs builds metric attribute
  sets (`semconv.HTTPClient.MetricOptions`).

On an app request (about 2.5 ms in the two daemons on rc.328) the hop is
about 6%.

The 2026-11-09 report estimated the hop at roughly half of per-request CPU.
That counted every frame of the edge's proxy and every frame under
gatewayd-internal's `conn.serve`. The table above counts only the
transport.

### The transports, measured alone

`BenchmarkInternalHop` (`pkg/gateway/internal_hop_bench_test.go`) measures
one hop:
- a GET carrying the headers the edge adds;
- answered 404 with a 116-byte problem body;
- both ends in one process, `-cpu 1`, so ns/op is the hop's whole CPU.

It ran on `gregale-internal-test-1` (Xeon 2.80 GHz, Go 1.26.10), three runs
each.

| Variant | ns/op | B/op | allocs/op |
|---|---|---|---|
| Call the handler in the same process | 1,912–2,030 | 1,432 | 10 |
| Unix socket, HTTP/1.1 (today) | 30,384–30,607 | 6,593 | 77 |
| Unix socket, h2c | 46,270–47,114 | 8,338 | 79 |
| Unix socket, binary framing prototype | 7,870–10,212 | 1,663 | 31 |

The binary framing prototype:
- length-prefixed fields;
- the server decodes them into an `*http.Request` and calls the same handler;
- the client parses the status and headers into an `http.Header`.

It costs about a quarter of HTTP/1.1, and the two processes stay apart.
Removing the hop entirely saves about 6 µs more per request in this
benchmark. h2c costs half again as much as HTTP/1.1.

## Decision

1. **Binary HTTP on a second socket.**
   - A compute edge sends the requests it routes to its own node's
     gatewayd-internal over `/run/faas/gatewayd-internal-hop.sock`.
   - Each request is one binary HTTP message (RFC 9292, indeterminate-length
     form), so bodies stream as chunks and 1xx responses and trailers have a
     defined encoding.
   - A connection carries one request at a time, as HTTP/1.1 keep-alive
     does, and the edge keeps a pool of idle connections. There is no
     multiplexing and therefore no flow control: a slow client backs up only
     its own connection.
2. **A drop-in transport.**
   - On the edge, the new transport is an `http.RoundTripper` behind
     `InternalReverseProxy`. Request rewriting, body replay for wake
     failover, the stream session and the response copy do not change.
   - A handler flush, or a full buffer, ends a chunk. The edge flushes to its
     client after each chunk of a body of unknown length, as it does today.
3. **The same handler and trust in gatewayd-internal.**
   - Each message is decoded into an `*http.Request` and served by the
     handler chain the unix HTTP listener runs, middleware included.
   - The ResponseWriter supports `Flush`, but not `Hijack`.
   - Requests carry exactly the headers the edge sends today (forwarding,
     trace, placement and dispatch headers). Routing, edge rules and trust
     decisions see the same request.
   - The socket gets the same owner and mode as `gatewayd-internal.sock`.
   - Header size and read timeouts match that listener's `http.Server`.
4. **Cancellation.**
   - The edge cancels a request by closing its connection, as the HTTP/1.1
     transport does.
   - gatewayd-internal watches each connection while a handler runs, and
     cancels the request's context when the connection closes, as net/http's
     background read does.
5. **What keeps today's transport:**
   - upgrades: WebSocket and other `Upgrade` requests already take the edge's
     separate upgrade path over HTTP/1.1;
   - requests to another node's gatewayd-internal (placement and the compute
     pool, HTTP/1.1 over TCP);
   - schedd's synthetic requests on `gatewayd-internal.sock`;
   - the control-plane edge;
   - every request while the switch is off.
6. **Fallback, so release order does not matter.**
   - If the hop socket is missing or a dial fails, the edge sends that
     request over HTTP/1.1. An edge at release N works beside a
     gatewayd-internal at N−1, which has no hop socket.
   - A gatewayd-internal at N listens on the hop socket whether or not its
     edge uses it.
   - A failure after the request was written is handled as today's transport
     errors are.
7. **Switch.** `FAAS_EDGE_INTERNAL_HOP=http1|binary`, `http1` by default,
   like other data-path changes.
8. **Metric.** `gatewayd_public_internal_hop_fallback_total{reason}` counts
   requests that fell back to HTTP/1.1. The reasons are `no_socket` and
   `dial_error`. It gets a spec §12 row.
9. **Acceptance.** Turn the switch on for one compute node. Compare it with
   switch off on the same node, using the `miss` scenario of
   `tests/load/edge` at 450 requests per second and a 30 s profile of both
   daemons. The binary hop stays only if both hold:
   - the two daemons' CPU per 404 falls by at least 80 µs, about 10%;
   - the journeys suite is green with the switch on: streaming responses,
     SSE, uploads, WebSocket, and wake failover with a replayed body.

   Otherwise the switch, the transport and the listener are removed. If it
   passes: both nodes, then the default flips after one release.

## Consequences

- **Expected saving.** About 120 µs per request: 14% of the two daemons'
  CPU on a 404 and about 5% on an app request. That is the benchmark's ratio
  applied to the profile's 160 µs. Step 9 measures the real number.
- **What stays the same:**
  - two processes, as ADR-070 and ADR-1343 set out;
  - their separate systemd hardening, so the edge keeps its stricter
    syscall filter and loads none of gatewayd-internal's sealing keys;
  - their independent releases.
- **The local hop's HTTP semantics now exist twice.** `Expect:
  100-continue`, `HEAD`, 1xx responses, trailers, declared lengths and header
  limits must behave as in net/http. The table-driven `InternalReverseProxy`
  tests run over both transports, and a conformance test sends the same
  requests through both and compares what the handler sees and what the
  client receives.
- **New code in `pkg/gateway`:** the RFC 9292 codec, the transport and the
  server adapter, plus a listener in gatewayd-internal's wiring.
- **Not covered here:**
  - **The `otelhttp` metric attribute sets on the hop,** 35 of the 47 µs.
    That is a small separate fix: pass the wrapper a no-op meter provider,
    or drop the wrapper's client metrics.
  - **gatewayd-internal's gRPC stream to vmmd,** about 23% of its CPU on an
    app request. It is the larger lever for app traffic and needs its own
    record.
  - **Overload shedding at the edge before the TLS handshake storm.** Its
    own record.
  - **The control-plane edge's unix hop, which defaults to h2c**
    (`FAAS_INTERNAL_H2C`, issue #675). The benchmark says HTTP/1.1 is
    cheaper there too; that default changes only after a measurement on the
    control plane.

## Rejected alternatives

- **One process: gatewayd-internal's handler inside the edge, or the edge's
  listener inside gatewayd-internal.**
  - **Gain over binary HTTP:** it saves about 6 µs more per request in the
    benchmark, roughly 40–50 µs (about 5%) in production once the
    `otelhttp` fix lands.
  - **Security:** the internet-facing TLS and HTTP/2 parser would share an
    address space with the keys only gatewayd-internal loads today
    (`host.age`, `fleet.age` and its sealed environment).
  - **Blast radius:** every gatewayd-internal release would restart the
    public listener and drop client connections. `cmd/gatewayd-internal`
    took 196 commits in the 31 days to 2026-11-10; `cmd/gatewayd-public`
    took 60.
  - **Failure coupling:** a crash or memory blow-up in routing, wake, log
    drains or edge rules would take TLS down with it. Each daemon has its own
    512 MB `MemoryMax` today.
  - **Effort:** `cmd/gatewayd-internal` is a `package main` of about 200
    source files of wiring, and it would have to become a library.
  - **Architecture:** it reverses the split of ADR-070.
  - Revisit if the binary hop fails step 9 and per-request CPU still sets
    the ceiling.
- **h2c on the unix socket.** Measured at 46.3–47.1 µs against 30.4–30.6 µs
  for HTTP/1.1.
- **gRPC.** HTTP/2 framing plus protobuf, so no cheaper than h2c.
- **An ad-hoc binary format.** RFC 9292 costs the same to encode and already
  defines 1xx, trailers and streamed content. The benchmark's prototype is
  ad hoc only because it covers no bodies or trailers.
- **Tuning HTTP/1.1 instead.** Per-request deadlines are about 3% of
  gatewayd-internal; the rest is parsing, formatting and the background-read
  machinery that the protocol itself requires.
- **kTLS with descriptor passing.**
  - Go has no kTLS.
  - One HTTP/2 connection carries requests that can belong to different
    nodes, so handing off whole connections would undo placement routing.
- **A shared-memory ring between the two processes.** It still needs its own
  wake-ups (futex or eventfd), and at these message sizes it saves little
  over a unix socket write, at much greater complexity.
- **Doing nothing and adding nodes.** Cost scales with nodes, and the
  per-node ceiling of a flood or a misconfigured client stays where it is.
