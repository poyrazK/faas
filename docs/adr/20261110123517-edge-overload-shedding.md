# ADR 20261110123517 · A compute edge sheds load before its node saturates

- **Status:** proposed
- **Date:** 2026-11-10
- **Amends:** ADR-20261103163954 §4 (a compute edge's client limits), adding a
  limit for the node as a whole beside the per-address ones.
- **Related:** ADR-20261107180000 (handshake gate shares),
  ADR-20261030100950 (CPU pressure backstop, the first reader of
  `cpu.pressure`), ADR-20261110081523 (binary hop),
  [the 2026-11-09 edge request cost report](../ops/evidence/20261109-edge-request-cpu/REPORT.md)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

On 2026-11-09 a load generator in us-east1 sent 404-bound requests to both
compute nodes, with the per-address limits lifted for the window.

- Each node served about 3,200–3,600 requests per second, then saturated.
- Past that point it collapsed instead of levelling off:
  - latency rose and the client opened new HTTP/2 connections (52,000 dials);
  - the TLS handshakes took the edge to 2.0–2.8 cores at 99–100% busy;
  - throughput fell.
- A customer app on compute-2 returned 77 extra 500s between 19:07:29 and
  19:08:01. They were the app's own errors while its node was starved of CPU.

A handshake with its first request costs the edge 2.2–2.9 ms of CPU. A
request on an open connection costs both gateway daemons about 0.84 ms. So
once clients start reconnecting, each unit of work costs about three times
as much, and the node never catches up.

The per-address limits (ADR-20261103163954 §4) were off in that run. With
them on, they would have stopped it: one address may open 100 connections a
second and send 2,500 requests a second. They don't protect the node from
many addresses:

- **Two addresses at their own limits already pass the node's ceiling.**
  So do a few hundred real users of a popular app, a crawler fleet, or a
  small botnet.
- **The node-wide handshake gate counts handshakes in progress, not CPU.**
  It allows 128 per CPU, 512 on a 4-vCPU node. At about 2 ms of CPU each,
  the node's CPUs are full at roughly 2,000 handshakes a second. The gate
  refuses only once 512 are in progress, which at that rate means each takes
  about a quarter of a second, well after saturation.
- **The per-host limit (10,000 requests a second) is above the node's
  ceiling,** by design: it bounds one host where no plan limit applies.

Nothing limits the node's total work by its CPU. Cutting per-request CPU
moves the ceiling (rc.336 cut 12%, #6905 and ADR-20261110081523 cut more)
but does not change what happens past it.

The plan for a large distributed flood is the flood fallback: move the host
back behind Cloudflare (ADR-20261103163954). That takes minutes. Until it
lands, and for floods too small to warrant it, the node itself has to stay
up and keep serving its customers.

## Decision

1. **An overload level per edge.**
   - The compute edge measures three signals every 100 ms, each smoothed
     over the last second:
     - **edge scheduling delay:** the 99th percentile of the time its
       goroutines waited to run (`/sched/latencies:seconds` from
       `runtime/metrics`). This is saturation inside the edge process.
     - **node CPU pressure:** the share of time any task waited for a CPU,
       from the `some total` counter in `/proc/pressure/cpu`.
     - **gateway CPU share:** the CPU that the edge and gatewayd-internal
       used, from their units' cgroup `cpu.stat`, as a share of the node's
       CPUs.
   - The edge is **overloaded** when its scheduling delay passes its
     threshold, or when node CPU pressure passes its threshold while the
     gateways use more than their share threshold. The second condition
     stops a CPU-hungry tenant from making the edge refuse traffic that
     isn't what fills the node.
   - A level from 0 to 1 follows the signal:
     - up by `EdgeOverloadLevelStep` on each overloaded tick;
     - down by `EdgeOverloadLevelRecoveryStep` on each tick below 70% of
       every threshold;
     - unchanged in between.

     It rises within a second and falls over several, so it doesn't
     oscillate.
   - The thresholds and steps live in `pkg/api/limits.go`. Their starting
     values come from observe mode (decision 7), not from this record.
2. **New connections are refused before TLS first.** At level L, the
   handshake gate resets a new connection before its handshake with
   probability L.
   - A floor of `EdgeOverloadHandshakesFloorPerCPU` handshakes a second is
     always admitted, so new users can still connect at the deepest level.
   - A reset costs the node microseconds; a handshake costs about 2 ms.
   - The per-address limits and the gate's in-progress caps apply first,
     as today.
3. **Requests on open connections are refused next, in a fixed order.**
   Each refusal is a 503 problem with code `edge_overloaded` and a
   `Retry-After` of 1–3 s, jittered, written by the edge. The connection
   stays open. The order:
   - **Unknown hosts:** a host no app has answered for is refused first,
     with probability min(1, 2L). The edge already keeps that set for its
     per-host limit (ADR-20261103163954 §4): a host enters it with its first
     2xx, 101 or 304 answer, and a full set forgets hosts idle for 10
     minutes. This catches 404s for names with no app, and leaves apps that
     have served, warm or parked, as known.
   - **Heavy addresses:** each address's request limit is scaled down from
     `EdgeClientRequestsPerSecondPerAddr` toward
     `EdgeOverloadRequestsPerSecondPerAddrFloor`, by 1 − L. The heaviest
     senders reach their limit first, and light users keep being served.
   - **Everything else:** past level 0.5, every remaining request is
     refused with probability 2L − 1.
4. **A refused address that keeps sending is cut off.**
   - A refusal skips the hop and gatewayd-internal but still pays the edge's
     TLS and HTTP/2 work: about half a served 404. A node refusing at twice
     its ceiling would still be nearly full.
   - So an address with more than `EdgeOverloadRefusalsPerAddrMax`
     refusals in one second, while the level is above 0, is cut off for
     `EdgeOverloadCooloff`:
     - its next refusals carry `Connection: close`, which Go's HTTP/2
       server turns into a GOAWAY;
     - in-flight streams finish;
     - the listener resets its new connections before TLS.
   - The address is keyed as the request limits key it today: the peer, or
     CF-Connecting-IP behind Cloudflare.
5. **What is never shed:**
   - requests and streams already admitted;
   - WebSockets and other upgraded connections already open;
   - the edge's control listener (metrics, readiness).

   The edge never closes idle connections and never turns off keep-alive to
   save CPU. Each of those forces a handshake, the most expensive work it
   does.
6. **Scope.** This applies to a compute edge's public TLS listener and the
   requests on it (`FAAS_EDGE_MODE=compute`). The control-plane edge and
   gatewayd-internal don't change. The edge sheds before forwarding, so
   gatewayd-internal sees less work, and the tenant slice gets the CPU the
   edge no longer uses.
7. **Switch.** `FAAS_EDGE_OVERLOAD=off|observe|on`, `off` by default, set
   by the manifest's `public_edge.overload`. It needs the client limits
   (`public_edge.front: none`).
   - `observe` computes and exports the signals and the level and refuses
     nothing.
   - Rollout: `observe` on both nodes for 7 days of normal traffic,
     including the :00 and :30 cron waves; then the thresholds go into
     `pkg/api/limits.go`; then `on` for one node, then both.
8. **Metrics.** Each gets a spec §12 row:
   - `gatewayd_public_overload_level`, a gauge from 0 to 1;
   - `gatewayd_public_overload_signal{signal}`, with `sched_delay_p99_seconds`,
     `node_cpu_pressure` and `gateway_cpu_share`;
   - new reasons on the existing `gatewayd_public_edge_refused_total`:
     `overload_connection`, `overload_unknown_host`, `overload_address`,
     `overload_request` and `overload_cooloff`.

   A warning alert fires when the level stays above 0 for 2 minutes. Its
   runbook entry is the flood fallback or another node.
9. **Acceptance.** All of these must hold:
   - **Observe mode:** the level stays at 0 through 7 days of normal
     traffic and cron waves. Any false positive means the thresholds are
     wrong.
   - **Benchmarks:** a reset before TLS and a refused request cost what
     this record assumes (microseconds, and about half a served 404).
   - **One overload run on one node,** like the 2026-11-09 run: the same
     generator, about twice the node's ceiling, the per-address limits
     lifted for the window. It needs the owner's OK at the time. It passes
     if:
     - the node keeps serving at least 80% of its ceiling;
     - the customer apps on it see no extra 5xx;
     - the level returns to 0 within 30 s of the load stopping.

## Consequences

- **What clients see under overload:** a fast 503 with `Retry-After`
  instead of a timeout. The unknown hosts, the heaviest senders and new
  connections are refused first.
- **No reconnect storm.** Fast refusals keep each connection's open streams
  low, so HTTP/2 clients have no reason to open more connections.
- **A brand-new app ranks with the unknown hosts** until it answers once.
  Apps that have served, warm or parked, are known and are refused only in
  the later stages.
- **Carrier-grade NAT:** an address that carries many users loses more of
  its share as the level rises, and can be cut off. It already has a
  per-address limit; overload makes that limit tighter, not new.
- **Not a flood defence.** A flood spread across thousands of light
  addresses looks like real users and is refused only in the last stage
  (decision 3). The flood fallback stays the answer to that. This record
  keeps the node alive until it lands.
- **New code:**
  - the controller and its signal sampler in `pkg/clientlimit`;
  - a request-shedding layer between the per-address and per-host limits,
    sharing the per-host limit's known hosts;
  - a refusal step in the handshake gate;
  - the cool-off set.
  Each is table-tested with a fake clock and fake signals.

## Rejected alternatives

- **A fixed node-wide cap on requests in flight.** Request durations range
  from milliseconds to minutes: SSE, uploads, and wakes held for up to 30 s.
  A count of requests in flight doesn't track CPU. It would refuse slow,
  cheap requests and admit fast, expensive floods.
- **Adaptive concurrency from hop latency** (Envoy's adaptive concurrency
  filter, Netflix's gradient limiter). The hop's latency includes each app's
  own run time, which varies by app, so a slow app would read as overload.
- **Lowering the handshake gate's 128 per CPU.** It counts handshakes in
  progress, not their rate. A lower cap would refuse clients on slow
  networks first (330 ms handshakes from Istanbul passed 32 per address on
  2026-11-09), while fast clients still filled the CPU.
- **A node-wide new-connection rate in nftables.** The kernel can't see CPU
  or requests on open connections. A fixed rate is too low when traffic is
  cheap and too high when it isn't. The per-source rules stay as the
  backstop.
- **Forwarding overflow to another node.** The edge's TLS and HTTP/2 work is
  what saturates, and it stays on the overloaded node. Every node takes
  every app host, so the other node is likely loaded too.
- **Cloudflare in front of every host, always.** The direct entry decision
  removed that. It stays the flood fallback.
- **Only cutting per-request CPU.** Each cut raises the ceiling, but past it
  the node still collapses.
