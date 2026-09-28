# ADR-313 · GitHub push head recheck before reconciliation

- **Status:** accepted
- **Date:** 2026-09-27
- **Decision:** For GitHub branch-push deliveries, githubd checks that the event SHA is the current remote branch head before fetching source and checks again after fetch and scan, immediately before reconciliation. A changed head is ignored without mutating project state. An unavailable lookup remains retryable. Release tags keep their separate immutable-tag policy.
- **Why:** Fetching and scanning can take long enough for a branch to advance after the existing pre-fetch check. Applying the old event after that advance would briefly reconcile stale source and could enqueue builds for it.
- **Consequences:** Each eligible branch-push delivery makes two installation-scoped branch-head requests. A GitHub API outage delays the delivery rather than allowing an unchecked reconcile. A branch can still advance after the final read; eliminating that final remote-state race would require a stronger conditional operation spanning GitHub's ref and Gregale's local transaction boundary.
- **Rejected alternatives:** A single pre-fetch read leaves the fetch-and-scan window open. Checking after reconciliation detects staleness only after project state has already changed.
