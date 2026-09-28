# ADR-319 · Bounded managed realtime callback dead letters

- **Status:** accepted
- **Date:** 2026-09-27
- **Decision:** Cap realtimed's retained callback dead letters at 64 MiB by
  default, separately from its 64 MiB pending callback budget. Allow an
  operator to set `FAAS_REALTIME_CALLBACK_DEAD_MAX_BYTES`. Evict the oldest
  dead-letter files when the byte limit is exceeded, including while loading
  an existing spool. Export retained bytes, capacity, eviction count, and
  last eviction time as node-local metrics, with alerts and a runbook.
- **Why:** ADR-282 moved dead letters to persistent node storage. Failed
  callbacks could then accumulate across reboots without a storage limit and
  exhaust the node's disk.
- **Consequences:** Retention can remove records before manual inspection.
  Operators must copy records needed for investigation or replay before
  lowering the limit. The outbox fsyncs dead-letter moves and eviction
  deletions. The oldest-first queue keeps eviction work logarithmic in the
  number of retained files. Eviction counters are process-local; the
  timestamp gauge lets Prometheus detect pruning that happens at startup.
- **Rejected alternatives:** Age-only retention does not bound disk use.
  Keeping dead letters indefinitely repeats the disk exhaustion risk.
