# ADR-20261115190000: A deleted managed PostgreSQL database's final window does not pause an account with headroom

- **Status:** accepted (owner, 2026-11-15: chose "headroom grace")
- **Date:** 2026-11-15
- **Owner:** apid (managed PostgreSQL) maintainers
- **Amends:** [ADR-20261104143000](20261104143000-managed-postgres-deletion-does-not-wait-for-corrections.md),
  which left the wait for the deletion window's coverage in place and named
  "reserve the deleted database's worst case" as the follow-up if it mattered.
  This ADR resolves that follow-up differently, with a headroom margin.

## Context

After ADR-20261104143000, deleting a database still blocks the account's new
databases, restores and resizes until the window the database was deleted in has
closed and been collected: up to an hour plus one collection sweep. Every create
in that time answers `503 managed_postgres_usage_stale`.

The customer journey run on production on 2026-11-15 hit this four times. The
Postgres journeys run in parallel, so one journey's delete blocked the others'
creates. A customer who deletes a database and creates its replacement sees the
same thing. A database created and deleted inside one hour was blocked even
more strictly: it had no reading at all, so it counted as `coverage_missing`.

The usage that window can still add is bounded. The database is stopped, so it
adds at most one window of one database's consumption. The guardrail this
protects is a monthly COGS ceiling, not an invoice.

## Decision

1. **A new, soft accounting issue: `final_window_pending`.** It applies to a
   deleted database whose only missing coverage is the window it was deleted in,
   while that window is open or closed no more than `usage.stale_after_seconds`
   ago. Every earlier window must already be covered. A database created and
   deleted in the same window also qualifies (it owes only that window), and so
   does one deleted in the window after its creation window if nothing earlier
   is owed.
2. **Admission passes through it while the account has headroom.** A pending
   final window blocks new reservations only when the account's recorded month
   is at or above 90% of any monthly ceiling: cost, compute, storage, history
   (when set) or egress, after the customer's own ceilings are intersected.
   Below that, the reservation is admitted.
3. **Everything else fails closed as before.** Unknown identities, a missing
   earlier window, an unconfirmed shutdown, window mismatches, active staleness
   and an overdue correction tail (ADR-20261104143000) block whatever the
   headroom. A final window that is still uncollected `stale_after_seconds`
   after it closed becomes `coverage_incomplete` and blocks again.
4. **Diagnostics.** `final_window_pending` appears among an item's reasons
   without making the item `blocking`, because whether it blocks depends on the
   account's totals. The usage page's `fresh` flag follows admission.

With the production policy (hourly windows, `stale_after_seconds` 10,800), a
database deleted at 12:17 no longer pauses an account that is below 90% of its
ceilings. If the 12:00–13:00 window is still uncollected at 16:00, the account
pauses until it is.

## Consequences

- An account can admit a new reservation before a deleted database's last window
  is recorded. The overshoot is bounded by one window of each such database's
  usage, and only up to the remaining 10% margin of each ceiling. Deleting many
  databases in one window adds one window per database; the per-plan database
  limit bounds how many can be live.
- `managed_postgres_usage_stale` after a delete now appears only near a ceiling,
  or when collection is more than `stale_after_seconds` late.
- No schema, API or configuration change. The 90% margin is a constant
  (`finalWindowHeadroomPercent`); it moves to configuration only if operators
  need to tune it.

## Rejected alternatives

- **Reserve the deleted database's worst case** (ADR-20261104143000's
  follow-up). Admitting against recorded usage plus the window's maximum
  possible consumption is the strictest bound, but the usage snapshot carries no
  per-database compute or storage limits, and egress has no per-window bound at
  all, so it would still need a separate rule.
- **Fix only the journey suite.** Running Postgres journeys serially hides the
  wait from the tests, but customers still wait up to an hour after a delete.

## Tests

`TestAccountingDiagnosticReasonsAndBoundaries` covers the pending window for a
covered tombstone, a database created and deleted in one window, one deleted in
the next window, an earlier missing window and a final window overdue by
`stale_after`. `TestFinalWindowPendingAdmitsOnlyWithHeadroom` checks admission
just under and at the margin of each ceiling, and that another database's hard
issue still blocks.

## Amendment 1 (2026-11-17): a live database's latest window

- **Context:** a live database's coverage counted as `coverage_incomplete`, a
  hard block, from the moment an hour closed until the sweep collected it. The
  provider publishes an hour's usage some minutes after the hour ends (prod
  sweeps still answered `managed postgres unavailable` 18 and 23 minutes past
  20:00 on 2026-11-16), so every hour began with the whole account's creates,
  restores and resizes refused (TestManagedPostgres resize and restore, prod
  journeys 2026-11-16). A database created in the hour that just closed had the
  same block as `coverage_missing`.
- **Decision (owner, 2026-11-17):** a live database whose only uncollected
  window is the most recent closed one, or which was created in it, reports
  `latest_window_pending`. Like `final_window_pending`, it is admitted while the
  recorded month is below 90% of every ceiling. Two or more uncollected
  windows block as before, and `observation_stale` still blocks a provider that
  keeps failing past `usage.stale_after_seconds`. The uncollected hour adds at
  most one window of one database's usage, the same bound this ADR accepted
  for a deleted database's final window.
- **API:** `latest_window_pending` joins the accounting diagnostic reasons
  (OpenAPI enum, SDK models).

