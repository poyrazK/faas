# ADR-516: Replay recent managed PostgreSQL usage corrections

- **Status:** accepted
- **Date:** 2026-10-03
- **Decision:** Recover missing usage first, then refresh previously collected
  windows within the last three completed policy windows, sharing the existing
  24-window per-database sweep budget. Require collection windows to be whole
  hours dividing a UTC day, and reject configuration duration overflow before
  converting seconds to Go durations.
- **Why:** Refreshing only the latest hour misses provider revisions after
  the next hour begins. Arbitrary window sizes either cannot be requested from
  Neon or cross UTC month boundaries, while monthly snapshots attribute an
  entire window to its start month. Large configuration integers can also wrap
  into valid-looking collection intervals or stale-data bounds.
- **Consequences:** With the default hourly policy, the latest three hours
  are replayed without increasing the existing per-database request ceiling.
  Forward recovery retains priority; replay runs newest first with remaining
  budget, never before established coverage, and never rereads a window already
  fetched in the same sweep. Restarts use durable coverage. Replacement retains
  the existing transactional ledger keys and observation ordering, including
  downward and zero corrections. A failed replay is reported as deferred and
  retains previously recorded complete evidence. Coverage freshness is not a
  guarantee that provider quantities are final. Valid window sizes are 1, 2, 3,
  4, 6, 8, 12, and 24 hours. No schema migration is required.
- **Rejected alternatives:** Replaying from creation every sweep repeats
  unbounded work and consumes provider request capacity. Replaying history
  before missing windows can indefinitely delay recovery. Silently prorating
  a window across months invents consumption distribution. Older revisions,
  retained exports, unavailable provider history, and organization-wide request
  budgets still need explicit reconciliation and scheduling work.

Neon's [consumption guide](https://neon.com/docs/guides/consumption-metrics)
describes delayed updates and the shared consumption request limit. Three
windows are a bounded correction policy, not a provider settlement guarantee.
See [ADR-492](492-managed-postgres-consumption-contract.md) for the unit and
complete-response contract. Existing installations with unsupported window
sizes must reconcile their ledger before adopting a different size; overlapping
window sizes remain rejected rather than silently double-counted.

Window identity compares timestamps with `time.Time.Equal`. PostgreSQL decodes
checkpoints with `time.Local`; comparing a UTC-normalized result to that raw
struct rejects the same instant and can stop collection after its first sweep.
PostgreSQL-backed and fixed-offset regressions cover resumed collection.
