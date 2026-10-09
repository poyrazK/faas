# ADR-726: Bounded workflow schedule catch-up

- **Status:** accepted
- **Date:** 2026-10-07
- **Extends:** ADR-487, ADR-637 and ADR-725
- **Decision:** Schedule triggers keep `catch_up: skip` as the default. Owners
  may opt into `catch_up: latest`, selecting at most one unconsumed occurrence
  inside `catch_up_window` after a gap in eligible evaluation. The window defaults
  to one hour and accepts durations between one minute and 24 hours. A current
  minute fire takes precedence; otherwise select the latest missed fire. All
  older occurrences are coalesced, rather than creating a replay queue.
- **Why:** A scheduler restart after a daily report's fire time should not silently
  lose the report when the owner explicitly wants recovery. Unbounded replay can
  consume shared capacity and repeat stale side effects after an outage.
- **Consequences:** The existing cursor is the high-water mark. An evaluation
  advances it through the observation time even when no fire is eligible, and
  preserves the last public outcome. Quota/overlap skips consume the selected
  occurrence and the interval, so freeing capacity does not retry an old skip.
  Run/cursor/history writes retain their existing atomic transaction and locks.
  History records the selected nominal fire separately from evaluation time;
  coalesced or expired fires do not get retroactive occurrence rows. Admission
  priority records the actual admission minute, not the old fire minute.
- **Rejected alternatives:** Replaying every missed fire (outage-sized backlog);
  changing the default (surprising side effects on upgrade); inferring recovery
  from retries (a missed start has no run or step to retry).

The policy and window remain owner-controlled even when a tenant customizes the
cadence. Both application and tenant schedules use the same calendar and recovery
rules. Tenant cadence updates, deployment/trigger changes, and dashboard automation
publication/pause/resume re-arm before firing. Tenant re-arming preserves saved
cadence, configuration version, and admission priority. No first observation
backfill is allowed, and a clock rollback cannot replay a consumed interval.

Recovery covers gaps in eligible evaluation, including scheduler/runtime downtime,
maintenance, and temporarily unavailable tenant links. It does not bypass any
account, app, tenant, runtime, overlap, or quota gate. The window includes a fire
exactly at its age limit. DST follows the canonical cron grammar. Admission does
not pin handler code or make step side effects exactly once.

This is an additive trigger/inspection contract with no new tables or migrations.
Upgrade validators and schedulers before publishing the new options: older
versions reject them. Before downgrade, remove the options from published
definitions and drain/cancel runs whose snapshots contain them.
