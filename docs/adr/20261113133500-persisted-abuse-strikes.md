# ADR-20261113133500 · Abuse strikes are stored, not counted in memory

- **Status:** accepted
- **Date:** 2026-11-13
- **Amends:** ADR-361 decision 6 ("Known limit": the escalation count is per schedd and in memory), ADR-20261112120603 (same limit, inherited)
- **Context:** The account abuse hold is placed on the `EgressFanoutHoldRecycles`-th (2) abuse recycle of one resource owner within `EgressFanoutHoldWindowSeconds` (1 hour). Recycles come from egress fan-out, egress flood and, since ADR-20261112120603, sustained CPU on Free. schedd counted them in a per-process map. ADR-361 accepted that a restart forgets the count. Two more gaps follow from it:
  - Each schedd owns only some apps. An account whose instances are recycled by different schedds is never counted twice by one of them, so the hold never lands however often its instances are recycled.
  - A deploy restarts schedd, so a recycle just before a deploy never counts.
- **Decision:**
  1. Every abuse recycle is a **strike** row in `account_abuse_strikes`: resource owner (account or organization id, so no foreign key), reason (`egress_fanout`, `egress_flood`, `cpu_sustained`; `operator` holds are placed directly and are never strikes), app and time.
  2. schedd records the strike, then counts the owner's strikes inside the window in a separate statement. Each count runs after its own insert commits, so of two schedds striking one owner at once the later count sees both: the threshold cannot be missed by a race. Two counts can both reach it; placing the hold is already idempotent.
  3. Each strike first deletes every strike older than the window. Nothing is kept past one window, so the table needs no retention job and keeps no account data for erasure.
  4. Thresholds, window, hold reasons and hold behaviour are unchanged. A store that cannot record strikes (test fakes) falls back to the old in-memory count.
  5. If recording the strike fails, the recycle has already happened. The error is returned to the caller like any other escalation failure and the hold is not placed for that recycle; the next recycle counts again.
- **Consequences:**
  - The hold now lands on the second recycle within the hour regardless of which schedd recycled the instances or whether schedd restarted in between.
  - One extra table with at most a few rows per abusive owner, written only on abuse recycles.
  - Release skew: an older schedd keeps counting in memory and writes no strikes. Until every schedd runs this release, a hold can need one more recycle than the threshold, as it could before.
