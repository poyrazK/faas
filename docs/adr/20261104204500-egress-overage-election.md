# ADR-20261104204500: Paid subjects elect to pay for egress above the allowance

- **Status:** accepted
- **Date:** 2026-11-04
- **Owner:** billing maintainers
- **Amends:** ADR-20261104090000 (decision 3: the egress ceiling), ADR-20261103131742 (the egress ceiling)
- **Related:** ADR-1430, ADR-1162, ADR-20261030093512, ADR-20261026120843

## Context

ADR-20261104090000 made every plan stop at its monthly egress allowance
(`MonthlyEgressGiB`) even when the provider bills egress live, because the
GA decision is that egress above the allowance is billed at $0.03/GiB only
for an account that opted in, and no opt-in existed. Paid customers who send
more than their allowance had no way to keep serving except a larger plan.

The election has to be safe to turn off. meterd delivers an egress hour to the
provider well after it ends (the pusher replays pending hours for up to the
billing lookback), so a plain flag read at delivery time could be toggled on
for a burst and off again before the burst's hours were billed.

## Decision

1. **The election is stored on the billing subject.** `accounts` and `orgs`
   gain `egress_overage_enabled_at` and `egress_overage_disabled_at`
   (nullable, with a CHECK that the end is not before the start). An
   organization that owns its resources elects for itself, like its spend
   cap. `state.EgressOverageStore` reads and writes it.
2. **It covers a period, not a moment.** An egress hour is billable as
   overage only when it starts in `[enabled_at, disabled_at)`. Turning it off
   stamps `disabled_at` once; turning it on clears `disabled_at` and keeps an
   earlier `enabled_at`. Hours from a gap that are still undelivered are then
   billed too, but the ceiling held during the gap, so only the overshoot
   before the stop is in them. The hour the election starts in is not
   covered, since part of it came before.
3. **Only paid plans may elect.** `PlanLimits.EgressOverageElectable` is true
   for Hobby, Pro and Scale and false for Free. Turning it off is always
   allowed, so a subject that moved to Free can end an election.
4. **meterd honours it in four places.**
   - The egress ceiling lifts only when the plan allows the election, it is
     on, and the provider bills the plan's egress live as of now
     (`billing.EgressOverageLifted`). A failed bulk read keeps every ceiling
     for that quota tick.
   - The pusher settles an egress hour the election does not cover at zero,
     like a Free hour; covered hours are billed as before.
   - The spend-cap hold, the compute reservation under the cap and the
     object-storage month close count only elected egress overage
     (`billing.ElectedEgressOverage`), so unbilled overshoot never uses the
     cap.
5. **API and CLI.** `GET`/`PUT /v1/account/egress-overage` and
   `/v1/orgs/{slug}/egress-overage` (`{"enabled": bool}`), guarded like the
   spend cap: read scope to read; admin scope, MFA and a recent step-up to
   change (organization owner for an organization). A plan that cannot elect
   gets 402 `plan_egress_overage_not_allowed`. The response reports the
   allowance, whether egress bills live and the price per GiB. Audit row
   `egress_overage.changed`. CLI: `gregale billing egress-overage [on|off]
   [--org SLUG]`.

## Consequences

- A paid subject keeps serving past its egress allowance once it elects and
  egress billing is live, billed per GiB within its spend cap. Without an
  election, nothing changes from ADR-20261104090000.
- While egress billing is off or in shadow, a saved election changes
  nothing; it takes effect when billing goes live.
- The dashboard needs a control for the election on the billing page
  (faas-web); until then the API and CLI are the ways to set it.
- The financial ledger's egress price snapshots and strict egress budgets
  still treat all egress above the allowance as priced. They stay
  conservative: a budget may count overshoot that is not billed.

## Alternatives rejected

- **A boolean flag read at delivery.** Turning it off before the pusher ran
  would drop overage already sent.
- **A history table of election periods.** Exact for every toggle, but the
  merged period loses only gap hours that the ceiling already stopped, and
  columns on the subject need no new table, purge step or foreign key.
- **Reuse the spend cap as the opt-in.** A NULL cap means "no limit", which
  every paid account has unless it chose otherwise; it is not consent to
  egress charges.
