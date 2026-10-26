# ADR-20261026120000 · Bridge stream headroom

- **Status:** Proposed
- **Date:** 2026-10-26
- **Amends:** [ADR-127](127-g19.1-h2c-bridge-hardening.md) §D2. The bridge's
  server-side cap of 100 streams per connection stays. What changes is vmmd's
  client: with the switch on, it stops forcing every request onto one
  connection.
- **Switch:** vmmd `bridge_stream_headroom` / `FAAS_BRIDGE_STREAM_HEADROOM`,
  default off.

## Context

In production a single small VM sometimes returned 503
`concurrency_queue_timeout` for every request for about 30 s while the VM sat
idle. Seen at 13:08 and 13:33 UTC on 2026-10-25 under ~100 req/s, both on
fsn-3. Reproduced at 22:58 UTC on 2026-10-26 with 350 closed-loop clients
against `h10-hello-node` (256 MB Node, one instance).

What the reproduction and vmmd's metrics show:

- **Where the time went.** vmmd's `stream_bridge_acquire` stayed at ~0 s. Its
  `bridge_h1_roundtrip`, the HTTP request from vmmd to the instance's stream
  bridge, took 15–27 s. That matched the whole forward's time.
- **The bridge received nothing.** During the reproduced stall it opened no
  guest connections, its idle ones closed (Node drops keep-alive after 5 s),
  and vmmd's unix socket to it had nothing queued. The VM's vCPUs were idle.
- **Release came from clients.** The stall ended at ~30 s, when the clients'
  own timeouts cancelled their requests down the chain.
- **Every request shared one connection.** vmmd reaches each bridge over one
  HTTP/2 connection, with `StrictMaxConcurrentStreams: true` against the
  bridge's 100-stream cap. Throughout, the bridge had exactly one connection.
- **The cap is per gateway.** Each gateway admits up to 80 requests per VM.
  With two gateways a VM can have 160 requests in vmmd (schedd's running
  reason read "160 request(s) are currently executing"). Anything over 100
  waits inside vmmd, where neither gateway can see it.
- **The guest pool churns.** The bridge keeps 32 idle guest connections but
  serves up to 100 streams. Above 32 concurrent requests it closes a guest
  connection after each response and dials a new one: ~90 new guest
  connections a second and ~940 TIME-WAIT sockets at ~290 req/s. At one point
  18 dials sat in SYN-SENT.
- **Slow dials hold slots for 30 s.** The bridge dials the guest with a 30 s
  timeout, so a dial that loses its SYN (retries at 1 s, 3 s, 7 s, 15 s) holds
  its stream, and the gateway slot above it, for up to 30 s.

The exact line on which the queued requests waited is not proven; that needs
a vmmd goroutine dump during a stall. The single strict connection is the only
place a request can wait without the bridge seeing it, and this change removes
that wait and the two amplifiers beside it.

## Decision

With `bridge_stream_headroom` on:

1. **More connections, same per-connection cap.** vmmd's HTTP/2 transport to
   a persistent bridge sets `StrictMaxConcurrentStreams: false`. A connection
   at the bridge's 100-stream cap no longer queues the next request; the
   transport opens another connection. The bridge still enforces 100 streams
   per connection. vmmd is the bridge's only, trusted client, and the gateways
   already bound requests per VM, so ADR-127's DoS bound holds.
2. **Guest pool sized to the stream cap.** vmmd starts bridges with
   `FAAS_BRIDGE_GUEST_HEADROOM=1`. The bridge then keeps up to 100 idle guest
   connections instead of 32.
3. **Short guest dial.** The bridge dials the guest with a 5 s timeout
   instead of 30 s. The guest is on the instance's own tap; a dial still open
   after two SYN retransmits is a stuck guest.
4. **New metric, always on.** vmmd records `bridge_stream_wait` in
   `vmmd_op_duration_seconds`: the time from the bridge request starting to
   its headers being written, which covers getting a connection and a free
   stream. It makes this queue visible whether or not the switch is on.

## Consequences

- With the switch off nothing changes except the new metric.
- With it on, a burst above 100 concurrent requests to one VM costs an extra
  unix-socket connection to its bridge instead of a hidden queue. Bridge
  memory grows with real concurrency, which the gateways' per-VM caps bound.
- The guest pool holds up to 100 idle connections per bridge for at most
  `h1IdleConnTimeout` (30 s) after a burst. The guest's Node server closes
  idle keep-alives after 5 s anyway.
- Not addressed here: the per-VM cap is still enforced per gateway (80 × N
  gateways), and scale-out still starts only at that cap. Both need their own
  decision.
- **Version skew.** An old bridge ignores `FAAS_BRIDGE_GUEST_HEADROOM`. An old
  vmmd never sets it. The switch only changes vmmd's own transport and the
  environment of bridges it starts.

## Verification

- Unit: the transport is strict with the switch off and non-strict with it
  on; the bridge environment carries the variable only when on; the server
  setter reaches managers created before and after it; the bridge sizes its
  pool and dial timeout from the variable; vmmd's TOML switch and
  environment override parse as the others do.
- Production, after a release: turn the switch on for one compute node and
  repeat the 350-client run on an app whose instance is on that node.
  - `bridge_stream_wait` should stay near zero instead of reaching seconds.
  - Requests the gateway admits should not stall for ~30 s.
  - Guest connection churn should fall: new connects per second and TIME-WAIT
    count in the instance's netns.
