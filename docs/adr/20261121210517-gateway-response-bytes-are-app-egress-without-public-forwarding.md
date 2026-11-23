# ADR-20261121210517: Gateway response bytes are app egress on nodes without public forwarding

Date: 2026-11-21
Status: Accepted
Related: ADR-046 (egress metering), ADR-1749 (public forwarding crosses the
funded interface), ADR-20261104132753 (edge bytes are app egress),
ADR-20261114100000 (internal egress is not billed), billing review R12-1

## Context

Billed app egress is `usage_minutes.net_tx_bytes`: the bytes an instance
sends across its veth. The allowance, the quota stop, overage, the spend
cap and the Polar push all read it.

Every production compute node runs the namespace-local bridge. vmmd dials
the guest from inside the instance's network namespace (ADR-028), so a
public HTTP or Upgrade response never crosses the veth. Billing review R12-1
(2026-11-17) found that these response bytes, most of an app's real egress,
are outside the allowance and never billed.

ADR-1749 fixes this by routing public bridges through the veth, but only
under vmmd's `native_process_recovery`. That mode is experimental and off in
production. It changes pmem, prepared networks and image staging, and makes
every vmmd restart drain, while production releases rely on no-drain
restarts. It also needs nftables 1.1.3 on the hosts. The owner chose not to
switch it on before the 2026-11-23 public launch.

gatewayd-internal already counts the response body bytes it forwards for
each instance: the HTTP path, streaming flushes and the raw Upgrade bridge
all record into the per-instance egress ring
(`pkg/gateway/egresssink`). meterd persists each frame exactly once into
`usage_minutes.tx_bytes`. That column was diagnostic only. Edge frames
(bytes served for an app without an instance) already count as egress
(ADR-20261104132753).

## Decision

1. meterd gets `[meter] gateway_response_egress` (env
   `FAAS_GATEWAY_RESPONSE_EGRESS`, code default off). When it is on, each
   persisted instance frame adds its bytes to `net_tx_bytes` as well as
   `tx_bytes`, in the same idempotent transaction
   (`ApplyGatewayUsageEvent`). The in-memory gateway stream that a
   single-box meterd uses bills them the same way in the sampler. Billing
   reads nothing new, so every consumer of `net_tx_bytes` picks the bytes up.
2. Production turns it on (`group_vars/control_plane/gateway_response_egress.yml`).
   The `control_plane_service` role refuses it while any compute node sets
   `faas_public_forwarding`, because public forwarding puts the same bytes on
   the veth and they would be billed twice. Turning on public forwarding
   later turns this off in the same change.
3. Private service calls stay unbilled. Service-call HTTP never recorded
   into the ring. The service proxy's raw (Upgrade) bridge now uses a
   forwarder without the ring (`nodeCache.ServiceRawForwarding`).
4. This amends ADR-1749's "gateway `tx_bytes` never become an invoice
   quantity" for nodes without public forwarding only. On a public-forwarding
   node the veth stays the single billing quantity.

## What is and isn't counted

- Counted: response bodies of 2xx/3xx buffered responses, every streamed
  flush, every raw Upgrade (WebSocket) chunk, and the bridge's synthetic
  error body on a failed Upgrade.
- Not counted: response headers, bodies of buffered 4xx/5xx responses, and
  private TCP proxy traffic. These understate egress slightly; none
  overstates it.
- Veth bytes are unchanged: outbound calls the app makes are still billed
  there, and internal destinations leave it under ADR-20261114100000.

## Consequences

- Customers reach their egress allowance on public response traffic, and
  paid accounts past it with overage on are billed for it.
- Meterd's in-flight gateway frames from before the flag turns on stay
  unbilled; there is no backfill.
- The legacy bridge keeps working; nothing changes on compute nodes.
