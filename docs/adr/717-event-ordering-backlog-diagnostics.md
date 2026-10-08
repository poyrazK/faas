# ADR-717: Event ordering backlog diagnostics

- **Status:** accepted
- **Date:** 2026-10-08
- **Decision:** Compute ordered routing blockers when reading the event backlog and share the blocker lookup with the claim gate.
- **Why:** A younger recipient can otherwise appear ready while an earlier event retries. Persisting blocker metadata would become stale when that earlier recipient recovers.
- **Consequences:** Backlog recipients expose the earliest unresolved lane recipient, its identity, acceptance age, routing state, next attempt, and receipt link. Consumer aggregates count ordering waits. The waiting reason filter applies before both pagination and aggregation. Raw event data and resolved keys remain absent.

Active routing and shared receipt leases precede ordering in waiting reason selection. Ordering precedes recorded capacity and retry backoff. These fields diagnose routing admission; invocation execution uses the existing work lane diagnostics. Whole-receipt routing excludes earlier positions in the same receipt because it processes its snapshot serially.

The PostgreSQL migration replaces the boolean claim predicate with a wrapper over the blocker lookup. Rollback restores the original predicate before dropping the lookup. The memory store likewise uses one shared matcher for claims and diagnostics.
