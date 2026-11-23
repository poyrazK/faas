# ADR 20261123140000 · The watchdog waits for vmmd's cold-boot verdict

- **Status:** accepted
- **Date:** 2026-11-23
- **Amends:** spec §6.1 (COLDBOOTING → STOPPED after 30 s), as enforced by the schedd watchdog
- **Related:** ADR-640 (WAKING budget includes the cold-boot fallback), ADR-20261114120000 (startup windows)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

Spec §6.1 gives COLD_BOOTING a 30 s budget. schedd's watchdog enforced it at
exactly 30 s (`ColdBootSweepBudget`). The cold boot's own vmmd call has a
35 s deadline (`ColdBootTimeout`: the 30 s budget plus jailer setup), and
that call returns the typed readiness verdict: `app_loopback_bound` when the
app listens on 127.0.0.1, or the port it listens on when that is not $PORT.

The watchdog fired first. On prod on 2026-11-23, a 127.0.0.1 app and an app
with a hard-coded port both failed with a generic `cold_boot_timeout`
("didn't become ready in time"), which was counted against the platform. The
customer never saw the actual cause.

## Decision

1. The watchdog stops a COLD_BOOTING row only after the §6.1 budget plus
   `coldBootRPCGrace` = `ColdBootTimeout − ColdBootSweepBudget + 2 s` (7 s
   today). The vmmd call always answers first. The watchdog remains the
   backstop for a call that hangs or never returns.
2. WAKING rows get the same grace on top of `WakingSweepBudget +
   ColdBootSweepBudget`, because a refused restore falls back to a cold boot
   (ADR-640).
3. A plan's startup window extension (ADR-20261114120000) still adds to the
   total.
4. The readiness window an app sees does not change
   (`api.ColdBootReadyWindowS` = 30 s). The grace only delays the watchdog's
   backstop, so the per-call verdict wins.

## Consequences

- A guest that never becomes ready keeps its RAM up to 7 s longer, at most
  ~37 s rather than 30 s. Admission already counts the instance during that
  time, so invariant 2 holds.
- Failures now carry the specific cause, so customers can fix them, and
  `build_success` no longer counts them as platform faults.
- If `ColdBootTimeout` changes, the grace follows it automatically.
