# ADR-20261104143000: Deleting a managed PostgreSQL database does not pause the account for its correction tail

- **Status:** accepted (owner, 2026-11-04: "yes write the ADR and fix")
- **Date:** 2026-11-04
- **Owner:** apid (managed PostgreSQL) maintainers
- **Amends:** [ADR-569](569-managed-postgres-terminal-usage-coverage.md): "New
  reservations remain blocked until missing history and the bounded final
  correction tail are covered" becomes "until missing history is covered, and
  again if the tail is overdue". [ADR-582](582-managed-postgres-accounting-diagnostics.md)'s
  `final_correction_pending` keeps its code and now means overdue.

## Context

The production test of managed PostgreSQL on 2026-11-04 purged a database and
then could not create another one for about three hours: every create, restore
and resize answered `503 managed_postgres_usage_stale`.

Under ADR-569, a deleted database blocks its account's new reservations until two
things are true:

1. **Coverage:** every window up to the endpoint (the end of the window the
   database was deleted in) has a reading. With hourly windows this is at most an
   hour, plus one collection sweep after the window closes.
2. **Final corrections:** each of the last three windows has been read again at or
   after the endpoint plus three windows, because a provider revises recent
   windows for hours (ADR-516).

The second wait is three more hours after every deletion, whether it is a purge,
a scheduled delete reaching the end of its grace period, or an operator delete.
A customer who deletes a database and creates another one finds the account
paused for three to four hours, and the message can only say "retry later".

The corrections do not add consumption that has no reading. They revise windows
that are already in the ledger, usually by a little. The guardrail they feed is a
monthly COGS ceiling, not an invoice.

## Decision

1. **Admission waits for coverage, not corrections.** A deleted database whose
   windows are covered through its endpoint no longer blocks admission while its
   correction tail is on schedule. The collector replays the tail exactly as
   before (ADR-516, ADR-569); the tombstone still completes only when every tail
   window has its later observation.
2. **An overdue tail fails closed.** The tail is due at the endpoint plus three
   windows (`correction_required_at` in diagnostics). Once that deadline has
   passed by more than `usage.stale_after_seconds`, the bound an active database's
   observation already gets, a tail window still missing its later observation
   blocks admission as `final_correction_pending`. A provider that keeps failing
   the replay, or a single failed window among corrected ones, still blocks.
3. **Nothing else changes.** Coverage gaps, unconfirmed shutdowns, unknown
   identities, window mismatches and active staleness block as before. Restore
   descendants still share their root's accounting. The monthly ceilings still
   compare the ledger as it stands.
4. **The customer message says how long.** `UsageStaleDetail` now says the pause
   lasts until the hour the database was deleted in has closed and its usage is
   collected, usually under an hour.

With the production policy (hourly windows, `stale_after_seconds` 10,800), a
deletion at 12:17 pauses the account until the 12:00–13:00 window is collected
shortly after 13:00. Before this change the pause lasted until about 16:00. If the
tail is still incomplete at 19:00 (16:00 deadline plus 3 h), the account pauses
again until it completes.

## Consequences

- A correction to a deleted database's last three windows can land after a new
  database was admitted. The next admission sees it. The overshoot is bounded by
  the size of the correction, not by any unrecorded window.
- The usage page's `fresh` flag and guardrail state follow admission, so they
  read fresh while the tail is still being replayed.
- `final_correction_pending` in diagnostics now appears only when the tail is
  overdue. Operators read `correction_required_at` for the on-schedule deadline.
- No schema, API or configuration change.

## Rejected alternatives

- **Shorter correction tail.** Replaying fewer windows would lose the late
  provider revisions ADR-516 exists to catch.
- **Reserve the deleted database's worst case.** Admitting against recorded usage
  plus the tail's maximum possible consumption would remove even the final-window
  wait, but the usage snapshot does not carry a database's size limits. Left as a
  follow-up if the remaining wait (under an hour) matters.
- **Keep the wait and only explain it.** The message already explained it; the
  three hours were still the problem.

## Tests

`TestAccountingDiagnosticReasonsAndBoundaries` covers a tail on schedule, overdue,
partly overdue and with a coverage backlog, and checks diagnostics and admission
agree. `TestUsageRetirementRecoversAndCorrectsFiniteWindowsAcrossRestart` (memory
and Postgres stores) admits once coverage is complete, keeps admitting while a
failed correction is on schedule, and blocks once it is overdue.
