# ADR-293 · Realtime callback outbox backpressure

- **Status:** accepted
- **Date:** 2026-09-27
- **Decision:** When a durable callback cannot fit in the pending outbox, retry
  admission at the queue retry interval until the callback context ends. If a
  message still cannot be persisted, close its WebSocket with status 1013
  (Try Again Later). Count rejected message and disconnect callbacks and page
  on any rejection.
- **Why:** The previous behavior continued reading client messages after the
  outbox rejected one. While the queue remained full, later messages were also
  read and rejected without notifying the client.
- **Consequences:** A message can still be rejected if capacity does not free
  before its callback deadline, but the client receives a retryable close and
  subsequent frames are not consumed. Disconnect callbacks use the same
  bounded admission wait. The fixed-cardinality rejection counter and alert
  identify any events that could not be persisted.
- **Rejected alternatives:** Continuing to read after a full-queue error can
  lose multiple messages on one live connection. Waiting without a deadline
  could pin connection cleanup indefinitely.
