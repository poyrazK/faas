# ADR-322 · Managed realtime callback backlog observability

- **Status:** accepted
- **Date:** 2026-09-27
- **Decision:** Export the age of the oldest pending callback and a
  process-local counter of successful durable replay deliveries. Alert when
  pending work is older than five minutes and no replay has succeeded for five
  minutes.
- **Why:** Pending count and bytes show backlog size but cannot distinguish a
  queue draining slowly from a replay loop that has stopped. Operators need an
  age signal and evidence of replay progress.
- **Consequences:** Callback records persist their enqueue timestamp so age
  survives retry rewrites and restarts. The age gauge is computed from a
  min-heap with lazy removal, keeping Prometheus scrapes from scanning every
  queued event. Replay deliveries count only successful acknowledgements made
  by the background replay workers; direct callback attempts are not counted.
- **Rejected alternatives:** Pending count alone cannot show how long the
  oldest callback has waited. A general callback success counter would include
  immediate deliveries and could hide a stalled replay loop.
