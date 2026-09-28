# ADR-341 — Resumable managed realtime channels

- **Status:** proposed
- **Date:** 2026-09-28
- **Decision:** Add an opt-in durable outbound channel log to managed realtime. Keep existing raw WebSocket frames and live-only `:publish` semantics unchanged. A versioned client protocol will later expose channel sequence numbers, application-authorized subscriptions, client acknowledgements, and bounded replay.

## Why

Today's `realtimed.Manager.Publish` selects node-local live subscribers and enqueues raw frames. A disconnected client has no subscription, and the publish response counts queues rather than client receipt. The persistent callback outbox is for client-to-application message and disconnect callbacks; it cannot supply missed application-to-client publishes. A process-local queue or channel-to-node route hint cannot provide continuity across reconnects or compute-node changes.

## Storage and ordering

`managed_realtime_channel_heads` serializes publishers for one endpoint and channel across apid replicas. The append transaction locks the head, assigns a monotonically increasing sequence, inserts the message, advances the head, and trims the bounded retained window before commit. An idempotency key deduplicates retries while its message remains retained. `managed_realtime_channel_messages` is a separate outbound log; it never reuses callback event sequence numbers, connection-owner leases, or channel-to-node route hints. A read observes the retention floor and page in one repeatable-read snapshot. A cursor below the floor must yield `history_unavailable`, not a partial page.

The initial store limit is 1,024 messages per channel, 4 KiB per message, and 32 channels per endpoint (128 MiB maximum payload storage per endpoint before row/index overhead). Messages remain available for up to 24 hours. Reads apply expiry immediately; append and a periodic reaper remove old rows and advance the retention floor. Expiry removes the entire sequence prefix through the highest expired message, preserving contiguous replay even if timestamps arrive out of sequence. An expired idempotency key may be reused. This store is not a customer-facing resume surface yet. Before enabling reconnect delivery for customer traffic, add plan entitlements and storage usage metrics. Numeric limits and billing need the normal product-registry and financial-model review.

## Client delivery contract

A future opt-in `gregale.realtime.v2` WebSocket subprotocol will carry `(channel, sequence, message_id, payload)` envelopes. It will retain the current raw-frame protocol for existing clients. A verified principal and stable consumer identifier scope acknowledgements; the server may only advance the stored acknowledgement for a sequence sent to that consumer. An acknowledgement attests client processing but cannot make client-side effects exactly once. Redelivery after a lost acknowledgement is allowed, so clients should deduplicate by message ID or sequence.

On reconnect, `realtimed` must authorize the channel before reading its history. It will register the new subscriber in a buffering state, capture a high-water sequence, replay the retained range above the acknowledged cursor, then drain buffered live messages above that mark in order. A full buffer closes the connection with a retryable reason; the durable log remains the source of truth. A cursor older than the retention floor produces an explicit `resync_required` response before live delivery. The cursor is bound to endpoint, channel, principal, and consumer identity; a connection ID is never a resume identity.

## Authorization

Endpoint authentication proves a client can connect, not that it may read every channel. A versioned subscription requires a channel grant from the application or an application authorization callback. The check binds endpoint, verified principal, channel, and permission before replay or live membership is granted. The existing app-scoped management API remains authorized by account and deployment-write scope. Shared static bearer tokens do not provide a distinct consumer principal; per-client durable acknowledgement therefore requires OIDC JWT or an application-issued stable identity.

## Rollout and recovery

1. Land the migration, state-store interface, in-memory parity, and PostgreSQL tests. No customer-visible behavior changes in this step.
2. Add a separately named retained-message management API that commits before replying and exposes cursor reads, gated by `FAAS_REALTIME_RETAINED_PREVIEW_ENABLED=1`. The private apid history RPC serves trusted daemon readers over a DAC-protected Unix socket or the split-box mTLS listener. This first storage surface does not deliver to WebSocket clients. Later node notifications are hints; node readers recover from missed notifications by polling. Existing live-only `:publish` keeps reporting queue admission.
3. Add the versioned WebSocket protocol, channel authorization, acknowledgement store, replay/live handoff, SDK support, and customer documentation behind an endpoint opt-in.
4. Qualify concurrent publishers, disconnect/reconnect across nodes, node restart, partial fleet availability, expired cursor, revocation, retention sweep, and slow-consumer recovery before promotion.

If a realtime node cannot reach durable history, it must fail the resumable subscription rather than silently treating it as transient. The cold-bootable application artifact and snapshot lifecycle are unaffected because messages live in the control-plane data store, outside customer VMs.
