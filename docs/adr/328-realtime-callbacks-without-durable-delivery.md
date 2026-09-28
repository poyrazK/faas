# ADR-328 · Realtime callbacks without durable delivery

- **Status:** accepted
- **Date:** 2026-09-27
- **Decision:** When `HTTPHooks` has no durable outbox and a message or
  disconnect callback fails, mark the event as not persisted. For a failed
  message callback, stop reading and close the WebSocket with status 1013.
  Count and page on these failures; production callback delivery should use a
  durable outbox.
- **Why:** The no-outbox `HTTPHooks` path sends directly to the receiver. If
  that request fails, the event cannot be replayed, but the manager previously
  kept consuming later frames as if callback errors were recoverable.
- **Consequences:** The client sees a retryable close after a failed message
  instead of the server silently consuming additional frames. Callers using
  this mode remain responsible for application-level retries. Durable outbox
  delivery failures keep their existing replay behavior.
- **Rejected alternatives:** Treating a direct HTTP delivery error as
  recoverable loses the event because there is no persisted copy to replay.
