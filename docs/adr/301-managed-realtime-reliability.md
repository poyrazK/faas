# ADR-301 · Managed realtime revocation and delivery outcomes

- **Status:** accepted
- **Date:** 2026-09-27
- **Decision:** Treat endpoint removal as revocation of its live sockets; reconcile
  durable endpoint intent against each active node's credential-free registration
  inventory; retain admission counters across endpoint policy updates; preserve
  per-connection callback order; expose partial fleet publishes.
- **Why:** A deleted endpoint could survive a missed removal because its durable
  row disappeared from the replay set. Existing sockets also continued to send
  callbacks. Re-registering an endpoint reset its connection counter, idle
  clients timed out between heartbeats, and a partially unavailable fleet could
  make a live connection appear gone or silently omit publish recipients.
- **Consequences:** The private realtimed API exposes only endpoint IDs at
  `GET /internal/endpoints`. The apid reconciler samples node registrations
  before reading durable rows and removes IDs absent from customer intent.
  Deleting or disabling an endpoint closes its existing sockets immediately on
  every node reached by the control-plane call; an unavailable node is repaired
  when it next responds to reconciliation. Endpoint updates retain the same
  admission counter, including when lowering the limit below current usage.
  Pong deadlines span the next heartbeat interval and response window.
  The callback outbox serializes pending events per connection by sequence,
  placing disconnect after the final message. A publish response reports the
  number of queues reached and whether any active node was unavailable; a
  partial success is not safe to retry blindly because reached nodes may
  receive a duplicate. An incomplete owner lookup returns a retryable
  availability error rather than asserting the connection is gone.
- **Rejected alternatives:** A finite delete tombstone can expire while a node
  remains offline and later rejoins with stale registration. A permanent
  tombstone creates unbounded state for a resource that has been deleted.
  Replaying in random event-ID order does not preserve WebSocket message
  order. Returning a generic failure for a partially delivered publish hides
  the queues that already accepted it and encourages duplicate retries.

The callback spool remains node-local. ADR-302 moves its default from
`/run/faas` to persistent host storage for reboot survival.
The public publish operation reports queue admission, not client receipt.
