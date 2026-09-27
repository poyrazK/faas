# ADR-288 · Bounded parallel managed realtime callback replay

- **Status:** accepted
- **Date:** 2026-09-27
- **Decision:** Replay durable callback events with a bounded worker pool. Use
  eight workers per `realtimed` process by default and allow operators to set
  `FAAS_REALTIME_CALLBACK_REPLAY_WORKERS`, capped at 32. Preserve ordering for
  each connection while delivering independent connections concurrently.
- **Why:** The durable outbox recovered callbacks serially. A slow receiver
  could therefore delay callbacks for every other connection after a restart
  or temporary receiver outage, even though their delivery order is
  independent.
- **Consequences:** Recovery can drain a backlog faster, subject to the
  receiver's capacity. Operators should size receiver concurrency across all
  realtime nodes. Retries, at-least-once delivery, and per-connection ordering
  remain unchanged. Callback concurrency is bounded per node.
- **Rejected alternatives:** One global lock preserves an unnecessary order
  between unrelated connections. Unbounded goroutines could overload callback
  receivers during recovery.
