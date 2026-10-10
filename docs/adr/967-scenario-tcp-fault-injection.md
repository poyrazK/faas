# ADR-967 · Scenario TCP fault injection

- **Status:** accepted
- **Date:** 2026-10-07
- **Amends:** ADR-423 (scenario-scoped HTTP chaos), ADR-576 (private TCP services)
- **Decision:** Apply bounded byte-stream faults at the existing private TCP
  service proxy for registered scenario workloads. Reuse run membership,
  authorization, fault leases, and developer-session cleanup. Add TCP latency,
  directional bandwidth caps, stalled forwarding, and abortive TCP resets.
- **Why:** Application resilience tests need to exercise slow, stalled, and
  reset database or service connections, including warm connection pools.
- **Consequences:** TCP rules explicitly name a port and select connections,
  rather than HTTP requests. HTTP rules retain their existing semantics.
  Active connections share a policy refresh loop per caller/target route;
  a removed or unauthorized run closes its streams. Expiry releases stalled
  and throttled traffic; a reset connection cannot be resurrected. Production
  streams never read scenario policies. Fixed-upstream relay fixtures run as
  isolated developer workloads, under normal tenant egress and plan policy.
- **Rejected alternatives:** Host-wide netem would affect unrelated traffic
  and introduce root-owned network mutation. HTTP status responses cannot
  represent database stream failures. A public or unrestricted host relay
  would bypass service identity and tenant egress policy.

## Transport semantics

The fault engine gates forwarding in chunks of at most 4 KiB, using the existing
bounded stream buffers. Latency delays each forwarded chunk; bandwidth waits
proportionally to byte count and provides backpressure. A timeout stops forwarding until expiry or policy replacement,
allowing the application's own timeout to fire. Reset uses an abortive close
on the caller's real TCP socket and cancels the upstream forwarding stream.
TLS bytes remain opaque. These faults do not model DNS failure, SYN loss,
  packet retransmission, or remote-side TCP reset propagation. Connect-phase
  timeout and refusal rules are applied after the guest handshake with Gregale's
  service proxy; they skip target dialing and are observed on first application
  I/O, not as a kernel SYN timeout or guaranteed `ECONNREFUSED` from `connect()`.

Fault percentages use a connection ordinal and seed. The decision stays fixed
for that connection across policy refreshes and alternate-replica retries;
parallel connection ordering and node placement can change the selected set.
Use 100 percent for repeatable assertions.

## Bounds and lifecycle

Existing 1-second to 5-minute leases and 16-rule limits still apply. Added
latency and reset delays are bounded by 30 seconds. Bandwidth is 1 to 1,000,000
KiB/s per connection and direction. Node-wide active policy routes are capped
at 256; polling is shared across all sockets on the same route and stops when
its final socket closes. Refreshes run every 250 ms with a 1-second lookup
deadline. Failed membership or policy reads close affected test streams.
Existing raw TCP session caps and byte/idle limits remain authoritative.

The private TCP listener, service-address DNS, vmmd admission and nftables
configuration must already be enabled together. No feature implementation
changes a fleet's network configuration automatically.

## Evidence

Portable tests cover real socket resets, both traffic directions, timing,
expiry, policy replacement, warm pooled connections, byte preservation,
half-closes, run removal and cancellation. API and store conformance tests
exercise every new rule kind. Native acceptance covers guest DNS, private
service routing, and faults through the real VM forwarding path.
