# ADR-332 · Backoff-aware realtime callback replay monitoring

- **Status:** accepted
- **Date:** 2026-09-28
- **Decision:** Expose the count of ready and retry-delayed per-connection
  callback heads, and count each durable replay delivery attempt separately
  from successful deliveries. Define a stalled replay as ready work that has
  received no attempt for ten minutes.
- **Why:** Pending age and successful deliveries alone cannot distinguish a
  replay loop that has stopped from callbacks correctly waiting for persisted
  exponential backoff or a `Retry-After` schedule. They also treat repeated
  delivery failures as no replay progress.
- **Consequences:** Operators can see whether callback work is currently
  eligible or still delayed. Ready and delayed gauges count per-connection
  heads, matching the scheduler's ordered work units and avoiding per-callback
  labels. Metrics remain node-local and process-local; attempts reset when
  `realtimed` restarts. Scraping stats promotes retry heads whose persisted
  schedule is due, so a stopped replay loop still exposes eligible work.
- **Rejected alternatives:** Alerting on pending age and successful delivery
  continues to report healthy scheduled backoff as stalled. Counting only
  successful deliveries still reports repeated, actively attempted failures
  as no replay activity.
