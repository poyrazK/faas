# ADR-20261031100000: Billing survives a Polar outage at boot, the spend cap counts what is billed, Free subjects leave the push scan

- **Status:** accepted
- **Date:** 2026-10-31
- **Owner:** billing maintainers
- **Amends:** ADR-20261029180000 (decision 4, the Polar preflight), ADR-1162 and ADR-20261030093512 (the spend cap's figure)
- **Related:** ADR-20261026120843 (spend cap holds), ADR-20261030120000

## Context

A code review of meterd and billing on main 71cb1ef161 (2026-10-31) found
three issues.

1. **A Polar outage at boot stops apid and meterd.** With billing on, both
   run Polar's catalog and organization preflight before starting and exit
   on any error, a timeout, a 5xx or a 429 included. A Polar outage during a
   deploy, crash restart or failover would take the whole API down, along
   with meterd's alerts, canary progression, Free quota stops and dunning.
2. **The spend cap measured usage above today's allowance, not what is
   billed.** The pusher bills each hour above the allowance in force at that
   hour and never revisits an hour (ADR-20261030093512): after a downgrade,
   usage the larger allowance covered is not back-billed; after an upgrade,
   overage already billed is not credited. The cap (meterd's hold, schedd's
   wake gate, `CurrentMonthOverageCents`) and the pusher's compute and egress
   clamps instead used month usage above the current allowance:
   - after an upgrade the clamps restarted from the larger allowance, so a
     month could be billed past its cap, which the docs promise is a hard
     ceiling;
   - after a downgrade the hold counted the covered usage. A Pro account
     moved to Hobby at 800 GB-h saw $2 of overage that is never billed,
     Hobby's default cap, and was parked at once.
3. **Cancelled subjects stayed in the push scan.** A cancelled subject keeps
   its billing identity, but the pusher never delivers a Free subject's
   hours, so they stayed pending: every hourly pass re-aggregated 30 days of
   their usage, and because the pending query returns each subject's oldest
   hour first under a 10,000-row limit, enough of them would have filled the
   limit ahead of paying subjects' current hours.

## Decision

1. **Only a configuration Polar cannot satisfy refuses to start.** The boot
   preflight (`loader.polarBootPreflight`) still refuses a catalog that fails
   validation, a missing token and an HTTP 4xx other than 429. A timeout,
   connection failure, 429 or 5xx (`polar.IsTransient`) starts the daemon and
   retries the preflight in the background, from 30 s backing off to every
   5 minutes, until it passes. A configuration failure found by a retry is
   logged as an error on every attempt; it cannot stop a running daemon.
2. **The spend cap counts what the month is billed.**
   `state.MonthBilledComputeMBSeconds` splits the month at each plan
   activation's hour; within a segment the allowance is constant, so the
   segment bills its usage above that allowance. The sum equals what the
   pusher bills hour by hour. `CurrentMonthOverageCents`, and so meterd's
   hold, schedd's wake gate, the billing page and the spend alerts, reports
   it, and the pusher's compute and egress clamps reserve it. A month without
   a plan change computes exactly what it did before, with no extra read; a
   month with one reads its usage before each change.
3. **Pending billing windows list paid subjects only.** Both pending-window
   queries take identities whose account or company organization is on a
   paid plan. The pusher's own Free check stays as a guard.

## Consequences

- During a Polar outage at boot, checkouts, portal sessions and pushes fail
  and retry on their own paths; the daemons serve everything else. A
  configuration broken during the outage is caught by the background retry
  and logged, not by a refused start.
- A downgrade no longer spends the cap on usage that is never billed, and an
  upgrade can no longer bill past it.
- An account that downgrades to Free with undelivered paid hours loses them,
  as before: they now leave the scan instead of staying pending.

## Rejected alternatives

- **Keep refusing to start, but retry the preflight for longer.** Any bound
  still turns a long Polar outage into an API outage.
- **Read the cap from delivery receipts.** Legacy Stripe receipts hold raw
  usage, not overage, and hours not yet delivered would need the pusher's
  arithmetic anyway.
- **Settle Free hours with zero receipts.** Same outcome, but every Free
  subject's usage would still be aggregated on each pass before being
  settled.
