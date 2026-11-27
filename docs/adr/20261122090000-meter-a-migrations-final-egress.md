# ADR 20261122090000 · Meter a live migration's final egress

- **Status:** accepted
- **Date:** 2026-11-22
- **Amends:** ADR-20261104090000 (departed network readings), ADR-046
- **Related:** ADR-20261121210517 (gateway response bytes are app egress),
  ADR-20261104132753 (edge bytes are app egress)
- **Decided by:** owner, 2026-11-22 (public responses billed as egress, edge
  bytes "same as live", fix migration final egress)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

The owner decided on 2026-11-22 that billed egress is the gateway's response
bytes plus the guest's own outbound bytes, and that edge-served bytes count
like live responses. ADR-20261121210517 already bills gateway response bytes
as app egress on nodes without public forwarding, and edge-served bytes were
already billed (ADR-20261104132753), so both now count the same way and
caching no longer changes a bill. Nothing more is needed for those two.

The owner also chose to fix a live migration's lost egress. meterd kept one
network checkpoint per instance. A live migration moves the instance to a
node whose vmmd counts its bytes from zero; schedd still serves the source
node's final reading as a departed row, but meterd dropped it whenever the
destination already reported, and a destination counter above the source's
checkpoint was billed only for the difference.

## Decision

`meter_network_checkpoints.node_id` records which node's counter a checkpoint
holds (empty for checkpoints written before). meterd tags each cumulative
reading with its node and passes a migrated instance's departed source
reading (from a node other than its live row's) beside the live one. The
Store applies the source's final reading first, then the destination's:

- a reading from the checkpoint's node (or without a node) advances it as
  before;
- a live reading from another node is a new counter, all of it new usage;
- a departed reading from a node the checkpoint already left was applied
  before the switch and is ignored.

The node-aware path is an optional `state.NodeNetworkObserver`; stores and
fakes without it keep the previous single-checkpoint behaviour.

## Consequences

- A migrated instance's last bytes on the source node are metered once.
- A source node whose departed reading never reaches meterd (it left before
  meterd's next read and before schedd's retention) still loses its final
  bytes, as before.
