# ADR-307 · Realtime callback replay recovery observability

- **Status:** accepted
- **Date:** 2026-09-27
- **Decision:** Export a process-local counter for callback replay supervisor
  restarts. Warn when a node restarts replay at least three times in a rolling
  15-minute window for five minutes.
- **Why:** A replay supervisor can recover before the stalled-replay alert
  fires. Logs explain each failure, but a counter and alert reveal recurring
  storage or queue errors that briefly clear and return.
- **Consequences:** The restart metric has no variable labels and remains
  bounded to one series per realtimed target. The counter resets when the
  daemon restarts; Prometheus `increase` handles counter resets in the alert.
  Operators can distinguish an isolated recovery from recurring failures.
- **Rejected alternatives:** The existing stalled-replay alert measures lack
  of successful delivery and does not show replay loops that repeatedly stop
  and recover before a five-minute delivery gap.
