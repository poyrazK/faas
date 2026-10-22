# 1107: Realtime message expiry diagnostics

## Decision

Show original message TTL, absolute UTC deadline and load-time expiry status in
retained event inspection. Expired payloads and metadata are suppressed, matching
REST expiry behavior. Non-expiring rows are explicit. The displayed status is a
snapshot; refreshing the inspector observes later expiry.

Expose process-local atomic Stats counters and Prometheus
`realtimed_expired_checkpoints_total{transport="websocket"|"sse"}` and
`realtimed_expired_outbound_drops_total` using the existing collector prefix.
Count WebSocket checkpoints after successful queue admission, SSE checkpoints
after successful stream write, and expired live payloads on dequeue. Failed
admissions and metadata-only filter checkpoints do not count as expiry. Existing
queue-capacity drop counters retain their meaning. Counters count delivery
attempts per subscriber, not unique messages, confirmed client receipts or deleted
history rows. Aggregate across realtime nodes; process restart resets counters.

Node and Python clients expose optional synchronous expiry observers on validated
`message_expired` checkpoints, with only code, channel, sequence and transport.
Node fallback forwards the same observer through WebSocket and SSE. Hooks run
before cursor persistence/ACK and Python checkpoint coalescing. They are best
effort: callback exceptions cannot change cursor progression or payload delivery.
Do not use them for business effects or blocking operations. Observations can
repeat after reconnect or failed persistence and must not imply durable progress.
Expired payloads never reach ordinary application message callbacks.

Both SDKs provide constant-memory diagnostic collectors. They retain only
saturating counters for WebSocket and SSE observations, with no identifiers,
payloads, timestamps or credentials. Node snapshots use safe integers. Python
snapshots integrate with the existing whitelisted diagnostics exporter as decimal
string counts. Sharing one collector across reconnects keeps local observations;
creating a fresh collector resets them. They never deduplicate sequence numbers
across subscriptions or claim exactly-once measurement.

## Verification

Static Go builds, Node type checking, focused Python lint/compilation, generator
preservation/export checks and whitespace checks only. Runtime tests remain
deferred at the user's request. No migration is required.
