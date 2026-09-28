# ADR-341 — Resumable managed realtime channels

- **Status:** proposed
- **Date:** 2026-09-28
- **Decision:** Add an opt-in durable outbound channel log to managed realtime. Keep existing raw WebSocket frames and live-only `:publish` semantics unchanged. An operator-gated v2 preview exposes channel sequence numbers, application-authorized subscriptions, client acknowledgements, and bounded replay.

## Why

Today's `realtimed.Manager.Publish` selects node-local live subscribers and enqueues raw frames. A disconnected client has no subscription, and the publish response counts queues rather than client receipt. The persistent callback outbox is for client-to-application message and disconnect callbacks; it cannot supply missed application-to-client publishes. A process-local queue or channel-to-node route hint cannot provide continuity across reconnects or compute-node changes.

## Storage and ordering

`managed_realtime_channel_heads` serializes publishers for one endpoint and channel across apid replicas. The append transaction locks the head, assigns a monotonically increasing sequence, inserts the message, advances the head, and trims the bounded retained window before commit. An idempotency key deduplicates retries while its message remains retained. `managed_realtime_channel_messages` is a separate outbound log; it never reuses callback event sequence numbers, connection-owner leases, or channel-to-node route hints. A read observes the retention floor and page in one repeatable-read snapshot. A cursor below the floor must yield `history_unavailable`, not a partial page.

The initial store limit is 1,024 messages per channel, 4 KiB per message, and 32 channels per endpoint (128 MiB maximum payload storage per endpoint before row/index overhead). Messages remain available for up to 24 hours. Reads apply expiry immediately; append and a periodic reaper remove old rows and advance the retention floor. Expiry removes the entire sequence prefix through the highest expired message, preserving contiguous replay even if timestamps arrive out of sequence. An expired idempotency key may be reused. This store is not a customer-facing resume surface yet. Before enabling reconnect delivery for customer traffic, add plan entitlements and storage usage metrics. Numeric limits and billing need the normal product-registry and financial-model review.

## Client delivery contract

The opt-in `gregale.realtime.v2` WebSocket subprotocol carries `(channel, sequence, message_id, payload)` envelopes. It retains the current raw-frame protocol for existing clients. The preview uses a client-held cursor: the server acknowledges only a sequence it queued on that connection, and the client persists its last processed cursor for the next `subscribe`. Acknowledgement does not make client-side effects exactly once. Redelivery after a lost acknowledgement is allowed, so clients should deduplicate by message ID or sequence. Server-held acknowledgement state may be added later, with a separate retention and identity contract.

On reconnect, `realtimed` authorizes the channel before reading its history. It reads a consistent first page, registers the subscriber, replays the retained range above the client cursor, then polls the durable log for later commits. Ordered reads close the replay/live gap even if an append lands between registration and the first poll. A full output queue closes the connection with a retryable reason; the durable log remains the source of truth. A cursor older than the retention floor produces an explicit `resync_required` response before delivery. The cursor is meaningful only for the authorized endpoint and channel; a connection ID is never a resume identity.

## Authorization

Endpoint authentication proves a client can connect, not that it may read every channel. A versioned subscription requires an application authorization callback at `/realtime/authorize-channel`. The synchronous check binds endpoint, verified OIDC principal, channel, and read permission before the first history read. A missing callback fails closed. Its grant lasts for the connection; immediate revocation uses the existing connection-close operation. The existing app-scoped management API remains authorized by account and deployment-write scope. Shared static bearer tokens do not provide a distinct consumer principal and cannot use v2.

Browser clients may carry a signed OIDC JWT in a reserved WebSocket subprotocol because the native constructor cannot set `Authorization`. This requires a non-empty exact endpoint origin allowlist and a present matching `Origin`. The daemon bounds and verifies the JWT, rejects ambiguous credentials, removes the credential subprotocol before hooks and negotiation, and echoes only the v2 protocol. Operators must redact the request's `Sec-WebSocket-Protocol` header from ingress logs and use short-lived JWTs.

## Rollout and recovery

1. Land the migration, state-store interface, in-memory parity, and PostgreSQL tests. No customer-visible behavior changes in this step.
2. Add a separately named retained-message management API that commits before replying and exposes cursor reads, gated by `FAAS_REALTIME_RETAINED_PREVIEW_ENABLED=1`. The private apid history RPC serves trusted daemon readers over a DAC-protected Unix socket or the split-box mTLS listener. This first storage surface does not deliver to WebSocket clients. Later node notifications are hints; node readers recover from missed notifications by polling. Existing live-only `:publish` keeps reporting queue admission.
3. Add the operator-gated versioned WebSocket preview, channel authorization callback, client-held acknowledgements, and polling replay/live handoff. Add SDK support, plan entitlements, usage metrics, and fleet qualification before customer promotion.
4. Qualify concurrent publishers, disconnect/reconnect across nodes, node restart, partial fleet availability, expired cursor, revocation, retention sweep, and slow-consumer recovery before promotion.

If a realtime node cannot reach durable history, it must fail the resumable subscription rather than silently treating it as transient. The cold-bootable application artifact and snapshot lifecycle are unaffected because messages live in the control-plane data store, outside customer VMs.
