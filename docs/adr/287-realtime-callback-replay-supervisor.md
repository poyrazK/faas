# ADR-287 · Managed realtime callback replay supervision

- **Status:** accepted
- **Date:** 2026-09-27
- **Decision:** Keep the callback outbox replay loop under an in-process
  supervisor. When a replay pass stops unexpectedly, retry it after an
  exponential delay starting at one second and capped at 30 seconds. Process
  shutdown cancels both replay and backoff.
- **Why:** Storage and queue errors can stop a replay pass even though the
  daemon and its HTTP listeners remain healthy. Durable callbacks should resume
  after the underlying problem clears without requiring an operator restart.
- **Consequences:** Pending records remain in the durable outbox across replay
  attempts. Each unexpected exit is logged with its error and retry delay;
  ordinary shutdown does not trigger a retry or warning. Callback delivery
  errors remain handled by the outbox's existing per-event retry budget.
- **Rejected alternatives:** Restarting the daemon requires operator action and
  interrupts active realtime connections. Immediate retries can busy-loop while
  the filesystem or queue remains unavailable.
