# ADR-325 · Realtime callback outbox capacity warning

- **Status:** accepted
- **Date:** 2026-09-27
- **Decision:** Export the configured pending callback outbox capacity and warn
  when queued bytes exceed 80% of that capacity for five minutes.
- **Why:** The outbox rejects new callback records when its byte limit is
  reached. Pending bytes alone do not show how close the queue is to rejecting
  work, so operators need a utilization signal with time to restore delivery.
- **Consequences:** The capacity gauge is fixed-cardinality and also appears in
  manager stats. The alert compares pending bytes with the configured limit, so
  it remains correct when outbox capacity changes in code or configuration.
  Operators should correct receiver or storage problems and let replay drain
  the queue before it reaches its hard limit.
- **Rejected alternatives:** A fixed byte threshold would misreport utilization
  if the outbox limit changes. Pending age can detect slow delivery but does not
  indicate proximity to the enqueue limit.
