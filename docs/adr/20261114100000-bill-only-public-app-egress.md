# ADR-20261114100000: Bill only public-bound app egress

- **Status:** proposed
- **Date:** 2026-11-14
- **Milestone:** M7 billing launch readiness (billing review 2026-11-13, R8-2)
- **Amends:** ADR-046, ADR-20261103131742 (egress allowance and ceiling)

## Context

Billed app egress is `meter.MonthlyEgressBytes` over `usage.net_tx_bytes`.
That column is the host-side veth counter that schedd samples for each
instance, so it counts every byte a guest sends, wherever it goes. ADR-046
and ADR-048 introduced it as informational and not billed; it later became
the billed source and the input to the egress ceiling, with nothing excluded.

`docs/plans.md` promises to bill "data your apps send" to the internet. Today
these are also counted, billed at $0.03/GiB above the allowance, and can stop
a subject at its egress ceiling:

- uploads to Gregale object storage (the S3 gateway on compute nodes);
- app-to-app calls over services and private networks, counted on the sending
  side (and the reply on the other);
- connections to `pgproxyd` and managed PostgreSQL;
- realtime publishes, which are counted again when delivered
  (`SyncRealtimeEgressUsage`).

## Proposal

Count only bytes whose destination is outside Gregale. Every guest sits
behind `tap0` in its own netns (ADR-009) and its egress already passes the
per-netns nftables egress policy (deny 25/465/587, RFC1918, link-local and
metadata). Add two nftables counters to that chain:

1. `internal_tx`: destinations in the platform's internal sets (object
   gateway, pgproxyd and the database proxy, service and private-network
   addresses, the realtime endpoint, the node's own gateway address);
2. `public_tx`: everything else that the policy accepts.

vmmd reads `public_tx` with the veth counter and reports it as a new
`public_tx_bytes` usage column (expand/contract migration; any new wire
field is `omitempty`). meterd bills and enforces the ceiling on
`public_tx_bytes` once every node reports it, and falls back to
`net_tx_bytes` for windows from nodes that do not. `net_tx_bytes` stays as
telemetry.

## Consequences

- Customers are billed for the egress the plan page describes.
- The internal sets must follow the platform's addresses as they change; a
  missing entry overbills as today, and an extra one underbills.
- The change touches vmmd and the netns setup, so it needs `make test-metal`
  and `make leakcheck` on the native KVM hosts, and a release where every
  node reports the new column before meterd switches.
- Until it ships, the launch checklist keeps the egress meter `off` or
  `shadow` (gate 5).

## Alternatives considered

- **Subtract internal byte counts in meterd from the services' own logs.**
  The object gateway, pgproxyd and realtime know their bytes, but not every
  internal path does (app-to-app calls), and attributing them back to an
  instance and hour is error-prone.
- **Count at the edge (`gatewayd-public`).** That sees responses to inbound
  requests only, not outbound connections the app opens.
