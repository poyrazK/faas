# ADR-962: Complete subscription capacity-wait counts

## Status
Accepted.

## Context
ADR-961's local capacity watch relied on a bounded pending-recipient sample. More than 50 pending recipients caused loss of confirmation even when complete consumer aggregates were available from the same backlog read.

## Decision
Extend the existing backlog consumer aggregate with counts for effective capacity_consumer, capacity_app, and capacity_account waiting reasons. PostgreSQL counts observation.reason in the existing sqlc query; MemStore counts the same classified reasons before pagination. Preserve the existing capacity_waiting_recipients field's raw-scope semantics. The new fields respect existing filter and waiting-reason precedence: recipients currently paused, circuit restricted, ordering blocked, or otherwise classified are not counted as capacity waits solely because a recorded capacity scope remains.

Add optional capacity_waits to subscription delivery health with app/subscription binding, a separate observed_at, recognized all_pending_application_recipients coverage, completeness, pending total, and mutually exclusive scope counts. Populate it from the full consumer aggregate in the same repeatable-read backlog snapshot as the pending sample; an empty matching aggregate means complete zero counts. The existing query selects one app/subscription/application group, so recipient pagination never truncates these counts. Counts cover captured acceptance and backfill recipients currently retained in the live backlog, not historical events or predicted future resource admission.

Use these counts for local capacity watch confirmation independently of sample truncation. Require explicit completeness/coverage, exact app/subscription bindings, nonnegative counts, and a sum no larger than that snapshot's pending total. Apply the existing component freshness and advancing timestamp checks to the aggregate's observed_at. Missing or invalid aggregates lose confirmation; do not fall back to the sample or silently synthesize zero. This safely handles older servers omitting the optional field. Fresh complete zero counts may resolve a confirmed capacity condition even when the investigative sample is truncated. Duration still measures consecutive local sampled subscription observations, not continuous individual blockage.

## Consequences
Large subscription backlogs can open and clear capacity watch alerts without unbounded recipient downloads or additional queries. The bounded pending sample remains available for exact-event investigation. API, CLI, and Go/Node/Python SDKs expose the counts and freshness. No migration, quota, delivery mutation, durable alert change, or compliance registry change is needed. This supersedes ADR-961's requirement that the entire pending sample fit within 50 recipients for capacity monitoring.
