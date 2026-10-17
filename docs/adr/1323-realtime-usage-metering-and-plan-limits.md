# ADR-1323: Realtime is free, with fair-use limits

- **Status:** accepted (owner decision, 2026-10-14: "We are only selling compute times, we are giving all software mechanics for free to our customers… put a fair usage for both platform and customers").
- **Date:** 2026-10-14
- **Decision:** Managed realtime has no separate charge and no billed meter. Plans limit only what realtime holds on our servers: concurrent connections per account across the fleet, and at most half of one realtime node per account. Usage is recorded per endpoint for capacity planning and abuse review, not billing.
- **Why:** Gregale sells compute. A feature that is only software is free; a feature that holds disk, memory or storage gets a limit sized to that resource, as logs do. Realtime's messaging logic is software, but every open connection holds realtimed memory, which until now had no account limit.

## Context

- **Pricing principle.** Customers pay for compute (plan RAM + 8 MB per running second, §4.7). Software features are free; features that consume a real resource carry limits (for example, log retention and storage).
- **What realtime holds.**
  - *Connections.* An idle connection measured about 52 KB of realtimed memory (2,000 connections on 2026-10-14: 72 MB heap plus 31 MB goroutine stacks). realtimed runs with `MemoryMax=512M`, and the node default of 10,000 connections could not fit: realtimed would be OOM-killed before reaching its own cap.
  - *Retained messages.* Already bounded per endpoint: 24 hours, 1,024 messages per channel, 32 channels, 4 KiB per message; endpoints are capped per plan.
  - *Fan-out bandwidth.* Not attributed to accounts. Application egress is recorded as non-billable today.
- **Before this ADR,** plans capped endpoint counts only. Each endpoint could hold 10,000 connections on every plan, so one Hobby account (10 endpoints) could fill every node.
- **Realtime triggers (ADR-1322)** deliver messages as invocations, which already bill as compute.

## Decision

### Free, no meter

No `realtime_*` billing meter, price or overage. The earlier draft of this ADR proposed billed connection and message meters with a shadow period; the owner rejected billing because realtime is software.

### Plan limits

In `pkg/api/limits.go` (`RealtimeLimits`, generated into `docs/plans.md`):

| Plan | Endpoints per app / account | Concurrent connections per account |
|---|---:|---:|
| Free | — | — |
| Hobby | 2 / 10 | 200 (~10 MB of realtimed memory) |
| Pro | 10 / 50 | 1,000 (~50 MB) |
| Scale | 25 / 250 | 5,000 (~260 MB, half a node) |

The numbers follow the plan ladder and sit at or above common managed-realtime free and entry tiers, so ordinary apps never meet them.

### Platform protection

- **Node cap 6,000** (`RealtimeNodeConnectionsDefault`, `FAAS_REALTIME_MAX_CONNECTIONS`): about 310 MB of idle connections inside the 512 MB unit, leaving room for queued frames and subscription pumps. It changes only together with `MemoryMax`.
- **Per-node share 50%** (`RealtimeAccountNodeSharePercent`): one account may hold at most half of a node's connections, so even a Scale account at its allowance cannot fill a node.
- **Node outbound budget 64 MiB** (`RealtimeNodeOutboundBytesDefault`, `FAAS_REALTIME_OUTBOUND_NODE_BYTES`; added 2026-10-15). Every connection keeps its own copy of the frames queued for it, up to 4 MiB, so the per-connection limit alone allows 6,000 × 4 MiB ≈ 24 GiB on one node. A single 1 MiB broadcast to 100 slow subscribers queues 100 MiB. The node budget bounds the sum of queued bytes:
  - Above 75% of the budget, realtimed disconnects the connections holding the largest backlogs (at least 1/16 of the per-connection limit, 256 KiB by default) with close code 1013 until queued bytes fall to 50%. Those are the slow consumers; resumable clients reconnect and recover from retained history.
  - A frame that would exceed the budget is refused like a full per-connection queue: legacy clients miss it and resumable clients are told to reconnect.
  - realtimed runs with `GOMEMLIMIT=384MiB`, keeping 128 MiB of the 512 MB unit for stacks, socket buffers and runtime overhead, as gatewayd-public does. 6,000 idle connections (~310 MB) plus a full budget fit under it.
  - Metrics: `realtimed_outbound_node_bytes_limit`, `realtimed_outbound_node_budget_drops_total`, `realtimed_outbound_shed_disconnects_total`, next to `realtimed_outbound_pending_bytes`.

### Enforcement

- apid passes the account's allowance to realtimed with each endpoint (`AccountMaxConnections`); plan changes arrive with the next endpoint reconcile (30 s).
- realtimed counts connections per account. At admission it refuses when this node's count plus the other nodes' reported counts reaches the allowance, or when this node's count reaches the node share. Refusals are HTTP 429 RFC 7807 problems with code `realtime_connection_limit`, the limit, the observed count and a docs link.
- Every 5 s each realtimed sends its per-account counts to apid over the private `RealtimeFleet` service (`SyncConnections`) and gets the other nodes' counts back. apid stores them in `realtime_node_connections`; a node's counts stop counting after 30 s without a sync, and the node is removed after an hour of silence.
- **Fail open on the fleet part.** Without a fresh exchange, a node enforces the allowance against its own connections only. An apid outage never refuses connections a node could hold; the allowance then holds per node until the exchange recovers. Overshoot during normal operation is bounded by 5 s of concurrent admissions across nodes.

### Usage recording

Kept from the earlier draft: realtimed records per-endpoint usage per hour (per-minute peak connections, connection-seconds, delivered and received messages and bytes) in `realtime_usage_hours`, behind `FAAS_REALTIME_USAGE_ENABLED=1`. It is used to review the limits against real use and to spot abuse. Nothing bills from it.

## Rejected alternatives

- **Bill connections or messages.** Contradicts the pricing principle: the messaging is software, and the real cost (memory per connection) is better controlled by a limit than a price.
- **Per-node limits only.** Simpler, but the documented per-account allowance would silently multiply with the node count. The fleet exchange keeps the documented number true, and the per-node share still protects each node.
- **Refuse all connections when apid is unreachable.** Turns a control-plane outage into a realtime outage. Local-only enforcement keeps nodes protected and customers connected.
- **Count retained messages or bandwidth against a quota.** Retained storage is already bounded per endpoint, and bandwidth is not billed for apps either.

## Consequences

- Customers pay nothing extra for realtime and see clear limits on the plans page, with precise 429 errors at the limit.
- realtimed no longer risks OOM at its default node cap.
- Multi-node deployments must let realtimed exchange counts with apid (`FAAS_REALTIME_FAIR_USE_ENABLED=1` or `FAAS_REALTIME_USAGE_ENABLED=1`); otherwise the allowance holds per node.
- apid writes per-account connection counts only when they change, plus one heartbeat row per node every 5 s.
- Future: review the allowances after a few weeks of recorded usage; a node-wide budget for queued outbound bytes is a separate follow-up if slow consumers ever threaten realtimed memory.

## Rollout

Each part ships off and is enabled separately once realtime has been tested
end to end; with every flag off, realtimed and apid behave as before this ADR.

- `FAAS_REALTIME_FAIR_USE_ENABLED` (apid and realtimed): plan allowances, the
  per-node account share and the 6,000 node default. Off, apid registers
  endpoints without an allowance, realtimed counts accounts but refuses only at
  the node and endpoint caps, and the node default stays 10,000.
- `FAAS_REALTIME_OUTBOUND_BUDGET_ENABLED` (realtimed): the node outbound budget,
  shedding and the 384 MiB soft memory limit (unless `GOMEMLIMIT` is set). Off,
  only the per-connection queue limits apply.
- `FAAS_REALTIME_USAGE_ENABLED` (apid and realtimed): usage recording, and
  `GET /v1/usage/realtime`, which answers 404 while off.
