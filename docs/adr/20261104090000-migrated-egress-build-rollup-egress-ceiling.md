# ADR-20261104090000: A live stats row beats a departed one, build time reaches the daily rollup, and the egress ceiling holds while billed live

- **Status:** accepted
- **Date:** 2026-11-04
- **Owner:** billing maintainers
- **Amends:** ADR-20261103121407 (departed stats rows), ADR-20261103131742 (decision on the egress ceiling), ADR-048 §5 (the daily rollup)
- **Related:** ADR-046, ADR-1430

## Context

A fourth review of metering, on main after #6465 (2026-11-04), found three
gaps.

1. **Live migration counted egress twice.** ADR-20261103121407 has schedd
   serve a departed instance's last stats row for
   `api.MeterFinalNetworkReadingRetention` (5 min). A live-migrated instance
   keeps its ID. For those minutes the node it left serves the departed row,
   with that node's high cumulative counters, and the new node serves the live
   row, counting from zero. meterd's fleet merge put both rows in one map keyed
   by instance ID, so the last row seen won, and the order changed between
   refreshes. Every switch from the low reading to the high one exceeded the
   durable checkpoint and was metered as new bytes, and every switch back read
   as a counter reset counted in full. A drain could bill the old node's
   egress several times, burn the allowance and trip the egress ceiling.
2. **Build time never reached `usage_daily`.** The rollup summed
   `usage_minutes.builder_seconds`, which no writer sets; builderd records
   build time in `builder_usage`, one row per finished build (migration
   00068). `usage_daily.builder_seconds` was always 0, so a build-minute quota
   (a GA decision) had nothing to read.
3. **The egress ceiling fell away once egress billed live.**
   `EgressCeilingBytes` returned no ceiling when the provider's egress meter
   was in live mode for the plan, so every paid account's egress past its
   allowance became overage at $0.03/GiB. The GA decision is that egress
   above the allowance is billed only for an account that opted into egress
   overage; every other account stops at the allowance, as Free does. No
   opt-in exists yet.

## Decision

1. **Departed rows say so, and a live row wins.** `InstanceStatsRow` gains
   `bool departed = 13`; schedd sets it on the rows it serves for departed
   instances. meterd keys the fleet's rows by instance and keeps a live row
   over any departed row for the same ID, whatever order the nodes answer in.
   An instance only departed rows report is kept when exactly one node reports
   it. Two departed readings come from two nodes' counters; either could count
   bytes twice, so both are dropped and at most the final seconds go
   unmetered. An older schedd never sets the flag, and its rows count as live,
   as they did before.
2. **The rollup reads `builder_usage`.** Each window's `usage_minutes` rows
   and its finished builds (`finished_at` in the window, `kind <> 'none'`) are
   summed together per (subject, app, day). A build belongs to the subject
   that owns the app's usage: a company organization that owns its resources,
   or else the account. An app-day with only builds gets a row of its own.
   The overwrite semantics are unchanged.
3. **Every plan stops at its egress allowance.** `EgressCeilingBytes`
   depends only on the plan. Live egress billing no longer lifts the ceiling;
   only the per-subject egress overage opt-in, decided separately, will.
   Until then, live billing can only bill the overshoot between the crossing
   and the quota tick that parks the apps.

## Consequences

- A node drain no longer inflates metered egress, so it no longer burns the
  allowance or trips the ceiling. Egress in the final seconds of an instance that two nodes report as
  departed is lost; this happens only when it moved and then parked within
  five minutes.
- Daily usage pages and a future build-minute quota see build seconds.
  `usage_minutes.builder_seconds` stays in the schema and the rollup still
  sums it, so nothing changes if a writer appears.
- Paid accounts past their egress allowance are parked with
  `quota_exceeded_egress` until the month ends or the plan grows, as Free
  accounts are. The egress overage opt-in needs its own ADR and a plan entry
  in `pkg/api/limits.go`.

## Alternatives rejected

- **Have schedd stop serving a departed row once the instance runs
  elsewhere.** A schedd sees only its own node; the fleet merge is the first
  place both rows meet.
- **Keep the larger of two readings.** The two counters are from different
  interfaces on different hosts; neither is a continuation of the other, and
  the checkpoint would still swing between them.
- **Bill egress past the allowance for every paid plan once live.** It
  contradicts the GA decision and charges customers for egress they did not
  agree to pay for.
