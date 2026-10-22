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

- A resumable WebSocket subscriber (including a read-only ticket subscriber) adds `"occupancy": true` to its `subscribe` frame. It then receives `{"type":"occupancy","channel":…,"subscribers":N}` after subscribing and whenever the fleet-wide count changes, at most once per second per channel (`RealtimeOccupancyFrameInterval`). An unchanged count is never sent twice to the same connection.
- Each node adds its exact local count to the other nodes' counts. A compute-only node reads those from apid over the private `RealtimeFleet/OccupancyOperation` every 5 s (`RealtimeOccupancyRemoteRefresh`), up to 20 channels per call and 4 calls at a time. apid answers from a per-channel, per-node cache kept for 2 s (`RealtimeOccupancyCacheTTL`) and leaves out the asking node, so nodes watching the same channel share one fleet read.
- A new watcher's first frame waits up to 2 s for the other nodes' counts, so the number does not jump from local to fleet-wide. A failed read keeps the last counts and is retried after a full interval.
- A single-box deployment has one realtime node, whose count is exact, and never calls apid.
- A node watches at most 1,000 channels (`RealtimeOccupancyWatchedMax`), which bounds the refresh to 50 small apid calls per interval. Past it the subscription succeeds and the client gets an `occupancy_limit` error frame instead of counts.
- A frame that cannot be queued is retried on the next interval. Occupancy never closes a connection, unlike presence and control frames.
- An SSE stream asks with `?occupancy=1` and receives `event: occupancy` with the same JSON. The stream writes counts while it waits for new history; it holds one pending count, and a newer count replaces one not yet written.

## Rejected alternatives

- **Counts in the route directory.** Turns a coarse publish hint into a write per subscribe/unsubscribe across the fleet, still misses SSE and tickets, and breaks for endpoints that overflow to broadcast.
- **Scanning connections per read.** Exact, but each read would lock every connection on the node (up to 6,000); the index costs one map update per subscription change instead.
- **Publishing occupancy as channel messages.** Mixes platform metadata with customer history and retention.
- **Billing or limiting occupancy reads beyond the per-request channel cap.** Contradicts the pricing principle; API rate limits already apply.

## Consequences

- Occupancy is a point-in-time snapshot. A count read during a node restart can be partial, and says so.
- Ticket subscriptions and SSE streams are now visible in counts, so dashboards built on the connection inventory and on occupancy may differ while a connection is closing.

## Rollout

`FAAS_REALTIME_OCCUPANCY_PREVIEW_ENABLED` gates occupancy on apid (the REST
read, 404 while off) and on realtimed (the internal read, live frames and SSE
events; a subscribe frame's `occupancy` field is ignored while off). It stays
off until realtime has been tested end to end. The per-node index is always
kept, since it costs one map update per subscription change and is not exposed
while the flag is off.
