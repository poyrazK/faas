# ADR-646 · Controlled workflow schedule replay

- **Status:** accepted
- **Date:** 2026-10-07
- **Extends:** ADR-638 workflow schedule history, ADR-639 bounded catch-up, ADR-645 schedule preview
- **Decision:** Add an owner-scoped, read-only replay preview and an explicit replay action for up to 20 selected skipped schedule occurrences. Admit items in scheduled order through the ordinary active-run quota and overlap checks.
- **Why:** Schedule history explains quota and overlap skips, but a customer could not recover a selected occurrence after resolving the blocker.
- **Consequences:** Each occurrence stores a SHA-256 fingerprint of its effective workflow definition and an independent replay run identity. Replay requires the same live deployment, matching definition, active tenant link when applicable, enabled schedule and current quota. Tenant cadence overrides participate in the fingerprint. Preview is advisory; replay rechecks eligibility under the app admission lock. A selected occurrence can create at most one replay run, even after run retention. Legacy rows without a fingerprint remain inspectable but cannot be replayed. Replay preserves the original skipped outcome and uses its original nominal `scheduled_for` time.
- **Rejected alternatives:** Replaying all skipped history would hide the scope and ordering of recovered work. Replaying against a changed deployment or definition could repeat different business effects. Bypassing normal quota or overlap admission could overload an app or violate its schedule policy.
