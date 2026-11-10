# Compute edge request cost and ceiling (2026-11-09)

The compute nodes' request path, with no app behind it, was load-tested on
production. This report records:

- what one request costs before any app runs;
- where that cost goes;
- what was changed;
- what is left.

The procedure is in [EdgeLoadTest](../../../runbooks/EdgeLoadTest.md).

## Setup

- **Nodes:** `gregale-compute-1` (us-east1-b) and `gregale-compute-2`
  (us-east1-c), each n2-standard-4 (4 vCPU). They run the edge
  (`gatewayd-public` in compute mode), `gatewayd-internal`, vmmd and the
  tenant VMs. Release rc.331.
- **Requests:** `GET https://lt-edge-none.gregale.dev/`, a host that names no
  app, so the edge and `gatewayd-internal` answer 404 and no VM wakes. The
  client connects straight to each node's entry address.
- **Clients:**
  - k6 from Istanbul, about 160 ms away, for the capped steps;
  - a stdlib Go open-loop HTTP/2 generator on `gregale-internal-test-1`
    (us-east1-b, 2 vCPU, about 1.3 ms away) for the ceiling.

## Ceiling: about 3,500 req/s per node

- **Capped steps.** All 404, nothing refused, control-plane Postgres stalled
  under 8% of each step:
  - miss: 50 to 300 req/s per node;
  - handshake: 20 to 60 new TLS connections per second per node;
  - new host: 5 to 20 never-seen names per second per node, each costing about
    two control-plane reads.
- **Handshake limit.** At 80 new connections per second from one address, the
  edge refused some handshakes under `EdgeTLSHandshakesInFlightPerAddrMax`
  (32 in flight), as designed.
- **Ceiling run.** Per-address client limits off for the window, 5,000 req/s
  per node asked:
  - `gregale-compute-1` served 3,500 req/s at 92% busy;
  - `gregale-compute-2` served 3,234 req/s at 96% busy;
  - **about 7k req/s in total.**
- **Past the ceiling.** Latency rose, and the client opened new connections:
  52k dials instead of 16. The TLS handshakes took the edge to 2.0–2.8 cores at
  99–100% busy while throughput fell into 10 s timeouts. One customer app on
  `gregale-compute-2` answered 77 extra 500s while its node was starved.
  Nothing on the edge sheds load before this point once the per-address limits
  are off; many real clients would reach it with the limits on.
- **From the Mac.** The same run from the Mac reached only about 1,500 req/s,
  bound by the client (load average 17 on 12 cores, plus the 160 ms round
  trip). The nodes stayed 23–35% busy.

## Where a request's CPU goes

CPU profiles, 30 s each, from the daemons' debug sockets on
`gregale-compute-1`.

| Path | Edge per request | gatewayd-internal per request |
|---|---|---|
| 404 for a host that names no app, 450 req/s | about 0.62 ms | about 0.34 ms |
| App request (Node hello apps), 300 req/s, rc.328, 2026-11-07 | about 0.7 ms | about 1.8 ms |

Kernel networking, journald and the other daemons add roughly the same again:
the node was 92% busy at 3,500 404s per second.

**Edge, 404 path:**
- **Go's HTTP/2 server writing frames, about 25%:** each frame is flushed as
  its own TLS record and `write` syscall.
- **The proxy hop to gatewayd-internal over the unix socket, 24%.**
- The client-limit, trace and request-budget wrappers.
- The per-request idle-session goroutine, about 3%.

**gatewayd-internal, 404 path:**
- **The HTTP server's own read, write and deadline work, about 40%.**
- **`observe`, 14%.** Three quarters of it is building and writing the
  `gateway_request` log line, about 2 KB of JSON for every non-2xx answer.
  journald's per-unit rate limit (10,000 lines per 30 s) then suppressed about
  100k lines per node in 75 s, and with them every other line the daemon
  wrote.
- **Two host-policy lookups per request, about 7%:** route substitution, then
  the main path, each with two `context.WithTimeout`.
- The RFC 7807 body, about 4%.

**gatewayd-internal, app path:**
- **The gRPC stream to vmmd, about 23%.**
- **The trace wrapper's context lookups:** `context.value` alone is 6% flat,
  because a long chain of context values is walked for every lookup.
- **Guest-identity header scans, about 5%.**
- **Per-request JSON encoding, about 3%.**
- **Freeze bookkeeping, about 2%.**
- **Allocation and GC, about 11%.**

## Changes made from these measurements (separate PRs)

1. **The request-log budget.** `gateway_request` lines at Info (a cold request
   or any non-2xx) are bounded:
   - limit: `GatewayRequestLogLinesPerSecond` (100) with burst
     `GatewayRequestLogBurst` (1,000), in `pkg/api/limits.go`;
   - a line past the budget is never built;
   - `gateway_request_log_suppressed_total` counts it, and the next written
     line carries `suppressed_before`;
   - warm 2xx lines were already Debug.

   `BenchmarkObserveNotFound`: 5.95 µs and 9 allocations written, against
   2.53 µs and 5 allocations suppressed. In production the saving is larger,
   because the benchmark writes to `io.Discard` and skips the 2 KB `write` to
   journald. The budget also keeps a flood from making journald drop the
   daemon's other lines.
2. **No separate flush for the last chunk of a declared length.** The edge's
   response copy flushed after every chunk. Over HTTP/2 that sent a small
   response as three writes (headers, data, end of stream). The chunk that
   completes a declared `Content-Length` is now left to the server, which sends
   it with the end of the stream. Bodies of unknown length (SSE, chunked
   streams) still flush every chunk.

   `BenchmarkSmallResponseOverHTTP2` (512-byte body, TLS, one core):
   155.5 µs → 107.7 µs per round trip, client and server together (−31%).

## Left, ranked by expected gain

1. **The second HTTP hop.** The edge decodes HTTP/2, then sends HTTP/1.1 over
   a unix socket for gatewayd-internal to parse again. Together that is
   roughly half of all per-request CPU. Removing or cheapening it touches the
   ADR-070 split, so it needs an ADR.
2. **Overload shedding at the edge.** Refuse new connections or requests early
   (RST before TLS, or 503) when the node's CPU or in-flight work passes a
   line, instead of letting handshakes from retrying clients collapse
   throughput. Architectural: an ADR.
3. **The context-value chain in gatewayd-internal.** Move per-request state
   into one struct value instead of dozens of `WithValue` layers (6% flat,
   more cumulative).
4. **The gRPC stream to vmmd per request** (23% on the app path): stream reuse
   or a cheaper framing for small requests.
5. **Smaller items:**
   - the duplicate host-policy lookup, about 1% overall; it touches ADR-590's
     ordering, so it is left alone here;
   - the per-request idle-session goroutine at the edge;
   - the guest-identity header scans.

At about 0.1 ms per request the same two nodes would serve roughly 30–40k
req/s of this shape. The edge's per-address limits stay on in production
throughout.
