# ADR-1324: Realtime channel occupancy

- **Status:** accepted (owner direction, 2026-10-17: build live subscriber counts as the next realtime item).
- **Date:** 2026-10-17
- **Decision:** Gregale reports how many subscriptions a realtime channel has across the fleet. Each realtime node keeps an exact per-channel count; apid sums the nodes for `GET /v1/apps/{slug}/realtime/endpoints/{id}/occupancy`. Resumable WebSocket clients can opt into live occupancy frames on `subscribe`. Counts are free, like the rest of realtime (ADR-1323).
- **Why:** "N people watching" and room sizes are a common realtime need. Presence in the resumable protocol (ADR-369) already lists members who join presence, but silent subscribers, SSE streams and ticket readers were invisible, and nothing returned a count.

## Context

- A node already tracks subscribers in three places: legacy WebSocket channel members (`m.subscribers`), resumable WebSocket members (`m.resumeSubscribers`), and per-connection maps for read-only ticket subscriptions. SSE streams are not indexed at all.
- The channel route directory (`managed_realtime_channel_routes`) records which nodes have a channel, as a conservative publish hint. SSE and ticket subscriptions do not register routes, and very large endpoints fall back to broadcast, so routes cannot answer "how many".
- Fleet presence leases live in apid (`managed_realtime_presence_leases`), one lease per connection, grouped into members by `presence_scope`.

## Decision

### What is counted

- **Subscribers:** open subscriptions to the channel, on every transport: legacy and resumable WebSocket, read-only ticket subscriptions and SSE streams. One connection subscribed to a channel counts once.
- **Presence members:** distinct presence members from the fleet presence leases. A principal-scoped member with three connections counts once.

### Per-node index

realtimed keeps an exact `map[endpoint channel]count`, changed exactly where a connection's channel set changes (subscribe, unsubscribe, resume subscribe and remove, ticket subscribe) and by each SSE stream's lifetime. When a connection is removed its channels are released once; a subscription that races the removal is never counted. The index has its own leaf mutex.

### Fleet read

apid asks every active realtime node (`GET /internal/endpoints/{id}/occupancy?channel=…`) with bounded concurrency and sums the answers. Asking every node keeps SSE-only and ticket-only nodes in the count; a node answers in microseconds and a fleet has tens of nodes, not thousands. An unreachable node is reported, not guessed: the response carries `partial`, `nodes_queried` and `nodes_unavailable`, as the connection inventory does.

One request reads 1 to `RealtimeOccupancyChannelsMax` (20) channels.

### Live occupancy frames

Live frames are a second step in the same ADR; see "Live frames" below once built.

## Rejected alternatives

- **Counts in the route directory.** Turns a coarse publish hint into a write per subscribe/unsubscribe across the fleet, still misses SSE and tickets, and breaks for endpoints that overflow to broadcast.
- **Scanning connections per read.** Exact, but each read would lock every connection on the node (up to 6,000); the index costs one map update per subscription change instead.
- **Publishing occupancy as channel messages.** Mixes platform metadata with customer history and retention.
- **Billing or limiting occupancy reads beyond the per-request channel cap.** Contradicts the pricing principle; API rate limits already apply.

## Consequences

- Occupancy is a point-in-time snapshot. A count read during a node restart can be partial, and says so.
- Ticket subscriptions and SSE streams are now visible in counts, so dashboards built on the connection inventory and on occupancy may differ while a connection is closing.
