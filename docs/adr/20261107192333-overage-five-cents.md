# ADR-20261107192333: Compute overage is $0.05 per GB-RAM-hour

Status: accepted (owner, 2026-11-07). Amends ADR-1430 decision 0 (the
overage meter price) and spec §4.7.

## Context

ADR-1430 set compute overage at $0.01 per GB-RAM-hour. Two things have
changed since.

**Overage now costs less than the plans themselves.** A GB-RAM-hour inside
a plan costs more than one bought as overage:

| Plan | Price / included GB-h | Per included GB-h |
|---|---|---|
| Hobby | $10 / 200 | $0.050 |
| Pro | $30 / 1,000 | $0.030 |
| Scale | $99 / 3,000 | $0.033 |

At $0.01, a Hobby account using 1,000 GB-h pays $18 rather than upgrading to
Pro for $30. Nothing in the price moves a growing account to the next plan.
Gregale is a subscription platform: usage inside the plan should be cheap,
and usage beyond it should not be.

**The idle ladder cuts billed seconds.** ADR-20261107122928 and the unbilled
quiet pause (ADR-20261023171905) bill only the seconds an instance runs.
Production's last 24 h billed 81 GB-h, about 74% of it apps waiting out
their idle timeout. On the measured traffic a wake cycle's billed GB-h falls
to about 8–20% of today's, and the included GB-h of every plan now cover
about 5× more activity.

The fleet model (200 GB net RAM) shows two effects:
- the same RAM carries about 2.5× the traffic, which raises the margin on
  plan fees;
- billed GB-h per unit of activity falls about 80%, so overage income at
  $0.01 falls by the same amount.

**Competitors.** None of the competitors prices overage above in-plan
usage:
- Railway and Vercel include a usage credit equal to the plan fee and price
  usage beyond it at the same rates.
- Render charges a workspace fee plus always-on instances (~$0.017–0.029
  per GB-hour, every hour).
- Lambda and Cloud Run are pay-per-use. They charge about $0.05–0.06 per
  GB-hour of running time, CPU included.

## Decision

Compute overage is **$0.05 per GB-RAM-hour** (`api.OverageMillicentsPerGBHour
= 5_000`), billed only on the seconds an instance runs, as before.

- **Plans and included GB-h are unchanged:** $0/$10/$30/$99 and 20/200/1,000/
  3,000 GB-h.
- **One rate for every paid plan.** It is above each plan's price per
  included GB-h, so upgrading is always cheaper than overage:
  - Hobby at Pro's 1,000 GB-h pays $50 instead of $30;
  - Pro at Scale's 3,000 GB-h pays $130 instead of $99.

  Any rate above $0.035 does this.
- **Everything priced from the constant follows:**
  - the per-account overage cap in cents (`Plan.DefaultOverageCapCents`), so
    the cap in GB-h is unchanged;
  - the spend cap's compute share;
  - preflight and CLI cost estimates;
  - the generated `docs/plans.md`;
  - the providers' wire quantity (`billing.WireQuantityMillicentsPerGBHour`).
    Its unit stays one millicent, so Stripe and Paddle unit prices do not
    change.
- **Polar's compute price changes with it:** the `metered_unit` price on the
  compute meter must be 5 cents per unit (one unit is one GB-RAM-hour).
  `validateCatalog` checks the product against the constant, so the Polar
  products must be updated before billing runs live with this release.
- **Customer-facing text says $0.05/GB-h:** the dashboard pricing, upgrade and
  usage pages, the CLI and the API descriptions.

## Consequences

- **Billing in production is disabled** (`FAAS_BILLING_MODE=disabled`), so no
  existing charge changes. The new rate applies once billing is live. If any
  account is billed before this ships, give it notice before its next
  billing period.
- **The change is versioned, never retroactive.** meterd records a new
  compute price snapshot, effective from the minute it first sees the new
  rate (`recordFinancialPricing`). Usage before that minute keeps the
  $0.01 snapshot it was recorded under.
- **Rollout:**
  - Raising the rate never invalidates a stored snapshot
    (`computeRateFits` accepts a snapshot at or below the lease rate).
  - Two meterds on different releases would each record their own rate
    every minute. Roll meterd out at once, as the control-plane step of the
    deploy order already does.
  - A strict compute lease granted by a previous-release daemon at $0.01
    does not fund a $0.05 snapshot. Strict budgets may refuse it until the
    control plane runs the release.
- **Bills:**
  - An account that stays inside its plan pays the same.
  - An always-busy app that exceeds its plan pays 5× today's overage.
  - A bursty app pays about the same in total as at $0.01 before the idle
    ladder, because the ladder bills fewer seconds.
- **Default spend caps in dollars rise 5×.** A paid plan's default cap stays
  one more allowance of overage at the published rate: $10 / $50 / $150 for
  Hobby / Pro / Scale, up from $2 / $10 / $30. In GB-h they cover the same
  overage as before. The cap is shared across meters, so for an account with
  an object-storage support grant it now bounds stored bytes higher (Pro:
  1,716 GiB, up from 383 GiB). Plan storage ceilings do not change.
- **Paddle is not the production provider.** Its overage price is created
  from the rate per wire unit while its quantity is already in millicents,
  so the two disagree. That predates this change, but this change
  multiplies it. Reconcile it before Paddle bills anyone.
- **The upgrade path is the price signal.** Accounts above Scale go to sales
  (Enterprise), in line with the sales-led motion.
- **The financial model workbook** (`ex44_faas_financial_model.xlsx`) must
  carry the new rate. It is owner-maintained outside the repo, and this ADR
  is the record until it does.
- **Not changed here:** egress, object storage and managed PostgreSQL
  overage keep their own prices.
