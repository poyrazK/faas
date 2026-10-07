# ADR-647 · Selected unstarted workflow cancellation

- **Status:** accepted
- **Date:** 2026-10-07
- **Decision:** Add a bounded preview and an explicit selected-run action for cancelling queued workflow runs that have never started. The action rechecks app ownership, optional workflow name, pending status, and the absence of `started_at` while holding the same run lock used by dispatcher claims. A batch contains at most 20 unique run IDs and commits atomically.
- **Why:** Pausing an automation prevents new admissions but leaves already queued runs in place. Operators need a safe way to remove selected queued work without cancelling retries, parked waits, or runs that may already have produced side effects.
- **Consequences:** Preview is advisory. A dispatcher claim that wins after preview causes the action to report the run as started or no longer queued. Eligible runs transition to failed with `cancelled_at`; started runs remain individually cancellable. The CLI requires `--yes` on the action and repeats the selected IDs from preview.
- **Rejected alternatives:** Cancelling every pending run for an automation is too broad and can include previously started retries. Reusing the generic run-cancel endpoint would allow a queued selection to race dispatch without a dedicated eligibility recheck.
