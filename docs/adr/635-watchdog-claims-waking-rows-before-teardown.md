# ADR-635 · The watchdog claims a WAKING row before it tears the VM down

- **Status:** proposed
- **Date:** 2026-10-07
- **Amends:** spec §6.1 (watchdog budgets) and ADR-470 (teardown accounting).
  The COLD_BOOTING and SNAPSHOTTING sweeps are unchanged.
- **Decision:**
  - **Every WAKING row gets the restore + cold-boot budget (35 s).** A row is
    created WAKING only after a wake or warm-pool restore chose a snapshot,
    and vmmd falls back to a cold boot inside the same RPC when the restore is
    refused (ADR-005). The sweep no longer calls `LatestSnapshot` to decide
    whether a WAKING row is snapshot-backed.
  - **`KillStuck(waking_timeout)` claims the row first.** It moves the row
    WAKING→COLD_BOOTING with a compare-and-set, then calls Destroy.
    - A lost CAS means the owning wake published first. The watchdog does
      nothing.
    - A won CAS makes the owner's `PublishOwnedInstanceRuntime` fail its
      `state = waking` check. The owner then aborts, destroys and releases its
      own reservation.
    - COLD_BOOTING counts RAM and concurrency. A failed Destroy therefore
      keeps the reservation (ADR-470), and the cold-boot sweep retries the
      Destroy before it writes FAILED.
- **Why:** H5-14, on production-us (rc.245, 2026-10-07). Every step of a
  workflow failed with `503 app_unavailable`, while the app's only instance
  was RUNNING.
  - A restore was refused because the platform base had changed, and vmmd
    cold-booted instead. The wake took 7.7 s, 5.4 s of it materializing the
    2 GiB app layer.
  - The owner marks the refused snapshot stale before it publishes RUNNING.
    For 60 ms the row was a 7.7 s-old WAKING row of a deployment with no
    non-stale snapshot. The schedd on the other compute node swept in that
    window, applied the bare 5 s budget and destroyed the VM.
  - The owner published RUNNING 20 ms later. The watchdog's WAKING→COLD_BOOTING
    write was then refused as an illegal edge from RUNNING.
  - The RUNNING row had no VM for 10 minutes, until the idle reaper stopped it.
    Billing follows the row (§4.7), so the customer was billed 612 s at plan
    RAM + 8 MB (0.176 GB-h) for a VM that lived about 8 s.
    Each forward reached vmmd, got `NotFound` and answered 503 without logging
    anything. The ADR-191 divergence sweep reported the row every 30 s, but it
    is report-only.
  - `appMu` is process-local, so it cannot stop a sweep on another node. Only
    the row's CAS is shared by every schedd.
- **Consequences:**
  - A truly hung restore holds its WAKING row for 35 s instead of 5 s. The
    engine's own per-call deadline still ends the wake first; the watchdog is
    the backstop.
  - A WAKING row killed at 35 s is also past the 30 s cold-boot budget, so the
    same sweep fails its COLD_BOOTING row. Production already showed this
    two-step sequence on 2026-10-07 05:08.
  - On the WAKING path the transition event is written before Destroy rather
    than after it.
- **Rejected alternatives:**
  - Keep the snapshot lookup but mark the snapshot stale after publishing. The
    owner is not the only writer: a sibling wake's fallback, imaged's base
    convergence and the snapshot-miss backoff all mark rows stale, and any of
    them can open the same window.
  - Fence the row by rewriting `wake_id`. That needs a new store method, and
    WAKING already has a RAM-counting successor state to use as the claim.
  - Turn on ADR-191 enforcement. Repairing a ghost after 30 s is a mitigation,
    not a fix, and enforcement still needs its week of counter evidence.
