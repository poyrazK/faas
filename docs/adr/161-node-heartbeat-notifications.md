# ADR-161: Preserve routing connections across node heartbeats

Status: Accepted

## Context

ADR-028 uses `compute_node_changed` to invalidate routing clients after node
configuration or availability changes. The trigger also fires when schedd only
advances `last_heartbeat_at`, or repeatedly marks an unavailable node unavailable.
Gateway invalidation closes the cached gRPC connection and can abort a customer
wake RPC. A live SSD test returned HTTP 503 immediately after the healthy node's
client was evicted, while its VM had successfully restored in 89 ms.

## Decision

Suppress UPDATE notifications when the complete old and new rows are identical
after excluding `last_heartbeat_at`. INSERT and every other effective row change
retain the existing payload and notification behavior. Compare complete rows so
new configuration fields invalidate by default. Node lifecycle transitions and
the independent `compute_node_keys` notification trigger remain effective.

Heartbeat timestamps and heartbeat history continue to be stored. Consumers that
need a current heartbeat timestamp must read the row; this invalidation channel
does not provide a heartbeat tick. The admin SSE connection has its own keepalive.

## Consequences

Normal liveness probes preserve active gateway and scheduler connections and
avoid redundant fleet key reloads. Real target changes and node deactivation
still invalidate immediately, including during a heartbeat update. This does
not add RPC replay, change node admission, or make transport failures impossible.
Rollback restores the previous trigger function without rewriting customer data.
