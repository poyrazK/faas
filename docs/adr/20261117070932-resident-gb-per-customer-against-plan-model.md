# Resident GB per customer warns against each plan's own model

Status: accepted

Amends: spec §12 (row "resident GB per paying customer | 0.305 | > 0.45
warn"), ADR-031's per-plan split of `meterd_resident_gb_per_customer`.

## Context

meterd reports month-to-date resident GB per paying customer for each plan.
`FaasResidentGbPerCustomerHigh` warned when any plan's average passed 0.45 GB
with at least five paying customers.

0.305 GB, and its 0.45 warn, are the blend the financial model expects across
the whole plan mix. A single plan's expected value is its planned resident
concurrency (spec §1: 0.02 / 0.15 / 0.60 / 3.00) times its instance size:

| Plan | Concurrency | RAM + 8 MB | Planned GB per customer | Warn above (1.5×) |
|---|---|---|---|---|
| Free | 0.02 | 264 MB | 0.005 | 0.008 |
| Hobby | 0.15 | 520 MB | 0.076 | 0.114 |
| Pro | 0.60 | 1,032 MB | 0.605 | 0.907 |
| Scale | 3.00 | 2,056 MB | 6.023 | 9.035 |

A Pro or Scale customer behaving exactly as the model plans is above 0.45, so
the flat threshold could not tell a healthy Scale cohort from a problem. In
production it warned from 2026-11-09 to 11-16 on five Scale accounts averaging
1.09 GB, a sixth of what Scale plans for. A Hobby or Free cohort, on the other
hand, could run at five times its model and stay under 0.45.

## Decision

1. `api.PlannedResidentConcurrency` and `api.PlannedResidentGBPerCustomer` in
   `pkg/api/limits.go` hold the per-plan model. The planned GB is the planned
   concurrency times `BillableRAMMB` of the plan's `RAMMB`.
2. meterd exports it as `meterd_resident_gb_per_customer_planned{plan}`, a
   constant for the release, beside the measured gauge.
3. `FaasResidentGbPerCustomerHigh` warns when a plan's average is above
   `api.ResidentGBPerCustomerWarnFactor` (1.5) times its planned value for one
   hour, still only with five or more paying customers. 1.5× is the margin
   validation V7 accepts. A meterd without the planned series keeps the alert
   quiet.
4. The blended 0.305 GB stays the §12 dashboard's fleet target and the number
   fed to the financial model monthly. It has no alert: with today's few
   customers it moves with the plan mix, not with density.

## Consequences

- The alert now says which plan is drifting from the model the prices rest
  on, which is the question the §12 row exists to answer.
- Customers who size apps below their plan's RAM read below the model; the
  alert errs quiet for them, which is the safe side for a warn.
- A change to a plan's RAM or planned concurrency moves its threshold with it.
  `pkg/promqlrules` pins the rule's factor to the constant.
