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

## Amendment 2026-11-15: slice 1 as built

The proposal's two counters became one, `faas_internal_tx`, on an
`egress_account` filter chain at postrouting priority 300 in each instance's
`ip faas` table. It counts packets that leave toward the veth in the
conntrack **original** direction (connections the guest opened) for
`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16` and `100.64.0.0/10`: the host
bridge (service proxy, DNS, relays) and any provider-verified private
destination. Running after the forward chain, it never counts a packet the
egress filter dropped. Private-network side-link traffic leaves through its
own interface and was never in the veth counter.

- Billed public egress is `net_tx_bytes - internal_tx_bytes`. The counter
  reads IP-layer bytes; vmmd adds 14 bytes of Ethernet framing per packet
  so the two subtract like for like.
- vmmd reads it in the existing 15 s per-namespace poll (no extra exec),
  accumulates deltas per instance across counter resets, and reports the
  cumulative total as `InstanceStats.internal_tx_bytes` and
  `InstanceTelemetry.internal_tx_bytes`.
- Replies to connections the guest accepted are not internal: an app's
  responses to its users through the gateway are egress. The service proxy
  and the public gateway both reach the guest from `gatewayd-internal`, so a
  callee's replies to another app's service call stay billed until a later
  slice attributes them on the gateway side (slice 3).
- Slice 2 carries the total through schedd to a `usage_minutes` column and
  lets meterd subtract it behind a meter setting that stays off until
  `make test-metal` and `make leakcheck` pass on the native hosts.

## Amendment 2026-11-15: slice 2 as built

- schedd relays vmmd's reading unchanged: `InstanceTelemetry.internal_tx_bytes`
  into its instance stats, and out to meterd as
  `InstanceStatsRow.internal_tx_bytes` (field 14, unset when vmmd has no
  reading, so older schedds and vmmds report nothing).
- Instead of a new `public_tx_bytes` usage column, meterd subtracts the
  internal bytes from `usage_minutes.net_tx_bytes` when it writes each minute.
  The subtraction happens inside the network observation, before the bytes
  are added: financial usage evidence is append-only, and the
  `retain_financial_usage_evidence` trigger rejects any decrease of billed
  usage.
  Every reader of billed egress (the monthly totals trigger, the egress
  allowance and ceiling, provider usage delivery, financial evidence) then
  follows without its own change, and a node that does not report the counter
  bills as before.
- `meter_network_checkpoints` gains `internal_tx_bytes`, `internal_tx_valid`
  and `internal_tx_pending` (expand-only migration). The checkpoint advances in
  the same transaction as the subtraction, so a retried minute subtracts
  nothing twice; a reading below the checkpoint is a counter reset and counts
  in full, as for the veth counters.
- The internal counter is polled every 15 s and the veth counter every
  250 ms, so a minute's internal bytes can exceed what is left of its egress.
  The excess carries to the next minute, at most one reading's worth; beyond
  that it is dropped, which bills slightly more, never less, than public
  egress. `net_tx_bytes` is never reduced below zero.
- `[meter] internal_egress_unbilled` (default false) turns it on. It stays
  off until `make test-metal` and `make leakcheck` pass on the native hosts
  and every compute node runs the vmmd from slice 1 (launch checklist gate 5).
  Once on, `net_tx_bytes` holds billed egress, and the raw veth total of a
  minute is no longer stored; the checkpoint keeps each instance's cumulative
  internal bytes for audit.

## Amendment 2026-11-15: slice 3 as built

- The service proxy in `gatewayd-internal` counts the body bytes each called
  instance writes back on an HTTP service call and records them in the egress
  sink as that instance's `service_reply_bytes`. A stale target's answer
  comes from the bridge, not the guest, and is not counted.
- `BytesFrame.service_reply_bytes` (field 8) carries them to meterd. With
  `[meter] internal_egress_unbilled` on, meterd persists them with the frame's
  idempotency row and adds them to `meter_network_checkpoints.service_reply_pending`,
  only for an instance whose node already reports internal egress (elsewhere
  they stay billed). The next subtraction takes them with the internal
  counter's delta; the carry is still at most what arrived since the last
  subtraction.
- The count is HTTP body bytes, so the callee's response headers and TCP/IP
  overhead stay billed: slightly more than public egress, never less.
- Upgraded (WebSocket) service calls: the service proxy marks the request's
  context, and the raw bridge records every chunk the callee streams back as
  service reply bytes as well as response bytes.
- Not covered: private TCP services (`service_tcp_listen`), whose copy path
  uses kernel splice that a counting wrapper would lose. Their replies stay
  billed.

## Amendment 2026-11-17: slice 3 withdrawn (billing review R12)

- Slice 3 subtracted bytes that were never billed. A service call from the
  gateway reaches the callee inside its network namespace: the bridge enters
  the namespace and dials the guest over tap0 (ADR-1749), so the callee's
  reply never crosses the root-side veth that `net_tx_bytes` counts. Only
  hosts with public forwarding route public bridges through the veth, and
  service calls stay namespace-local there too. Subtracting the reply bytes
  took the same amount of the instance's real outbound egress off its bill
  (R12-2).
- The gateway no longer counts service replies, meterd no longer queues them,
  and `meter_network_checkpoints.service_reply_pending` is reset and never
  read (the column stays; migrations are append-only).
  `BytesFrame.service_reply_bytes` (field 8) is deprecated and never set; it
  is removed after a release.
- The first internal reading of an instance whose egress checkpoint already
  exists sets the baseline only (R12-3). Without it, turning
  `internal_egress_unbilled` on while instances ran subtracted each one's
  whole internal history since vmmd started from its next minute, wiping out
  that minute's real egress.
- Open (R12-1): on hosts without public forwarding, which is every production
  node today, responses to public requests do not cross the veth either, so
  `net_tx_bytes` holds only connections the guest opens plus edge bytes. The
  gateway's response bytes (`usage_minutes.tx_bytes`) are not billed as
  egress. Which counter bills public responses is an owner decision.
- R12-1 was decided by the owner on 2026-11-22: billed egress is the gateway's
  response bytes plus the guest's own outbound bytes (ADR-20261121210517).

## Amendment (2026-11-22): internal egress unbilled on the fleet

The counter is live in production: every running VM on bhs-2 and bhs-3
(13 on 2026-11-22) has `faas_internal_tx` in its namespace and it counts
(up to 537 KB on one instance). `TestMetalInternalEgressCounted` passes on
the EC2 KVM test host, and its namespaces and links are gone afterwards (the
full `make leakcheck` waits for that host's concurrent soak to end).
`group_vars/control_plane/internal_egress_unbilled.yml` turns
`internal_egress_unbilled` on for the fleet through meterd's
`FAAS_INTERNAL_EGRESS_UNBILLED` drop-in. An instance without a valid
internal reading stays billed on its whole veth counter, and the first
reading after the switch is a baseline only (R12-3), so nothing billed
before is subtracted.

## Amendment (2026-11-23): service addresses, the database route and Gregale's own public destinations

Metering review r10 found guest bytes to Gregale-owned destinations billed as
egress:

- The ADR-576 service block (198.19.0.0/16) and the ADR-1027 database route
  (198.18.0.1) are translated on the host, not inside the namespace, so the
  guest's packets leave the veth still addressed to them. `faas_internal_tx`
  now also counts `198.18.0.0/15`. The rule counts only; it admits nothing.
- Managed Postgres runs on Neon, whose plan already includes 500 GB of
  egress per database (owner decision 2026-11-23: we do not bill app→database
  bytes again). Apps reach it at public `*.<region>.aws.neon.tech` addresses.
- Calls from an app to Gregale's public entry (`*.gregale.app`, object
  storage, the OTLP endpoint) are platform traffic; the receiving side bills
  them where due (gateway response bytes, storage usage).

vmmd takes `compute_node.internal_egress_cidrs` (IPv4 prefixes or addresses)
and `compute_node.internal_egress_hosts` (`FAAS_VMMD_INTERNAL_EGRESS_CIDRS` /
`_HOSTS` win) and adds them to every namespace's `faas_internal_tx` rule it
prepares. The prefixes and a first resolution of the hosts (bounded to 2 s)
apply before vmmd serves; hosts are re-resolved every 5 minutes. An address
stays internal for 24 h after DNS last returned it, and a failed lookup
keeps the previous set. Namespaces already running keep the set they were
prepared with until their next wake. vmmd_service's `99-faas-egress.conf`,
installed on every vmmd host, adds every compute edge's entry address from
the manifest (`faas_public_entry_addrs`, `faas_peer_entry_addrs`; behind
`*.gregale.app` and the DNS-only storage/objects/otel names), so a new node
is covered without a hand-kept list. Production adds the regional Neon host
(`group_vars/compute_nodes/internal_egress_destinations.yml`). Cloudflare-proxied names (`s3.gregale.dev`, `api.gregale.dev`) are
never listed: they resolve to shared Cloudflare anycast addresses, so traffic
to them stays billed. Neon's regional addresses are shared by every project
in the region, so a customer's own Neon database there is exempt too; that
is the accepted cost of not billing managed-database traffic twice.

The same review found the gateway billed by status, not by author: an app's
own 4xx/5xx body was never billed and the bridge's synthetic init-error 502
was. Gateway response bytes are now billed for every response the app wrote
and for none the platform wrote (a marked writer, or a problem carrying
`api.ErrorCodeHeader`, which both proxy paths strip from guest responses).
