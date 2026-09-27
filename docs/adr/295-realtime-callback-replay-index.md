# ADR-295 · Realtime callback replay scheduling index

- **Status:** accepted
- **Date:** 2026-09-27
- **Decision:** Keep pending callbacks in ordered per-connection queues and
  schedule only each connection's head. Select due heads from a ready heap and
  promote retry-delayed heads from a second heap when their persisted retry
  time arrives. Maintain these indexes on enqueue, claim, release, ack,
  dead-letter, replay, and startup load.
- **Why:** Rebuilding all connection heads and sorting them for every replay
  claim held the outbox mutex for work proportional to the complete pending
  backlog. A large backlog therefore reduced claim throughput for every replay
  worker. Per-connection ordering remains necessary to keep callback sequence
  and disconnect ordering intact.
- **Consequences:** Claiming a due callback takes logarithmic heap time in the
  number of ready connections. Appending a normally ordered event is constant
  time within its connection; out-of-order insertion scans only that
  connection's queue. Retry timestamps remain persisted in the existing
  records, and startup rebuilds the in-memory index from those records.
- **Rejected alternatives:** Sorting all pending events on each claim scales
  poorly under backlog. A single global ready heap cannot preserve per-
  connection ordering when the head event is delayed for retry.
