# ADR 20261122090000 · Bill public responses as egress

- **Status:** accepted
- **Date:** 2026-11-22
- **Amends:** ADR-20261114100000 (bill only public app egress; closes R12-1),
  ADR-20261104090000 (departed network readings), ADR-046
- **Decided by:** owner, 2026-11-22 ("Gateway + guest", edge bytes "same as
  live", fix migration final egress)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

Billed egress is `usage_minutes.net_tx_bytes`, the instance's host-side veth
counter. The gateway reaches an instance through vmmd's bridge, which enters
the instance's network namespace and dials the guest over `tap0`
(ADR-028/ADR-1749), so on a host without public forwarding (every production
node) a public response never crosses the veth. Billed egress held only the
connections the guest opened itself plus edge-served bytes: on the owner's
account in November, 1.17 GB of gateway responses against 0.119 GB billed. The
Free plan's egress allowance and paid egress overage therefore barely saw web
traffic, while a response served from the gateway's cache or by an edge rule
*was* billed (ADR-20261104132753), so enabling caching raised a bill.

Separately, a live migration lost the source node's last egress: meterd kept
one network checkpoint per instance and dropped the source's departed reading
whenever the destination already reported, and a destination counter above
the source's checkpoint was billed only for the difference.

## Decision

1. **Public responses are egress.** meterd adds the gateway's response bytes
   for an instance (`tx_bytes`: buffered, streamed and raw/WebSocket
   downstream bytes; route probes excluded per ADR-20261121231500) to the
   minute's billed `net_tx_bytes` as it writes the row. Billed egress is
   therefore gateway response bytes plus the guest's own outbound bytes
   (minus internal destinations once `internal_egress_unbilled` is on), and
   the financial ledger follows through its `usage_minutes` trigger. It is on
   by default; meterd's `public_response_egress_off` turns it off for a fleet
   whose nodes run public forwarding (native process recovery), where the
   bridge routes responses through the veth and they would count twice.
2. **Edge-served bytes count like live responses.** They stay billed egress,
   now consistent with live responses, so caching never changes the bill.
3. **Network checkpoints know their node.** `meter_network_checkpoints.node_id`
   records which node's counter the checkpoint holds. meterd applies a
   migrated instance's departed source reading (from a node other than its
   live row's) before the destination's, once: a live reading from another
   node is a new counter whose whole value is new usage, and a departed
   reading from a node the checkpoint already left is ignored. Readings
   without a node keep the previous behaviour.

## Consequences

- Web apps' responses now reach the Free 10 GiB egress allowance and, where
  the subject elected it, egress overage. Customer usage views show the
  higher egress.
- `public_response_egress_off` must be set before enabling native process
  recovery on a fleet; otherwise its responses are counted twice.
- A source node whose departed reading never reaches meterd (it left before
  meterd's next read and before schedd's retention) still loses its final
  bytes, as before.
