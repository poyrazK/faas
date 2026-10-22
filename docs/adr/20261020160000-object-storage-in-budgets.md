# ADR: Object storage in budgets

Date: 2026-10-20
Status: Accepted
Related: [ADR-566](566-financial-visibility-and-scoped-budgets.md), [ADR-1320](1320-object-storage-plan-allowances-ceilings-and-overage-cap.md), [ADR-156](156-object-storage-accounting.md), [ADR 20261019193500](20261019193500-budget-alert-channels.md)

## Context

Budgets (ADR-566) count only the meters of the financial ledger: compute and
interface egress. The cost report lists everything else as a missing bill
component. Object storage is billed (ADR-1320): once per UTC month, at month
close, from provider usage reports. A customer cannot set a budget on it, and
no threshold or projected-overrun alert ever counts it.

The ledger cannot simply gain an object-storage meter. Object-storage usage is
not sampled per minute by meterd. It arrives as cumulative provider reports
that apid imports, and three rates price it (stored GiB-months, requests per
million, egress per GiB). Its pricing lives in apid's object-storage
configuration, which meterd does not load.

## Decision

1. **apid records a live estimate.** Every
   `FinancialObjectStorageEstimateInterval` (5 minutes), apid's
   object-storage accounting loop records one row per billing account and UTC
   month in `financial_object_storage_estimates`. The row holds:
   - the month's cumulative usage (`state.SummarizeObjectUsage`, as the usage
     endpoint reads it);
   - the configured pricing;
   - the current plan's allowance (ADR-1320's live-estimate rule);
   - whether the usage was fresh;
   - `usage_through`, the oldest provider report the usage includes.

   An account without buckets records zero usage, so its budgets stay
   complete. Usage with unavailable meters is not recorded. Nothing is
   recorded without pricing or in gateway-safety mode. The newest observation
   replaces the row.
2. **The cost report reads it as a meter.** `billing.ReadFinancialCosts` adds
   an `object_storage` meter where an estimate exists. It is priced with
   `objectstorage.CalculateCharge`, as the usage endpoint and month close
   price it. Gross is before the allowance and net is after. It has one
   account allocation, because buckets carry no app or job attribution. The
   meter is stale when the estimate is older than
   `FinancialObjectStorageEstimateFreshness` (15 minutes) or the usage was
   not fresh. Known usage and the report scope include it.
3. **Forecast.** Each usage component is scaled by the month's length over
   the time since the period start up to `usage_through`. The projection is
   priced with the same rates and allowance, giving net and gross
   projections. As for ledger meters, there is none with less than a day of
   reports, or from stale usage.
4. **Budgets.** `object_storage` is a budget meter for monitored account
   budgets only. It cannot be used on resource scopes, because there is no
   attribution, or on strict budgets, because no reservation covers it.
   Thresholds, projected-overrun alerts and channel delivery
   (ADR-20261019193500) then cover it without change.
5. **Schema.** The policy spec check is widened in three expand migrations:
   add `financial_budget_policies_spec_v2_check` NOT VALID, validate it, then
   drop the narrower check. The previous release writes only compute and
   egress, which both checks accept.

## Consequences

- `GET /v1/billing/costs`, the forecast endpoint and the cost views show an
  `object_storage` meter once apid records estimates. Month close (ADR-1320)
  still produces the invoice line. The estimate is not an invoice and is
  not reconciled with it.
- The estimate uses the current plan's allowance, so a mid-month plan change
  can differ from month close, which takes the month's largest allowance.
  It is the same difference the usage endpoint already shows.
- Provider report lag makes the run rate slightly conservative: usage is
  divided by the reported time, not the wall-clock time.
- The overage cap (ADR-1320) is applied at month close only. A budget counts
  the uncapped estimate.
- Rejected:
  - **Per-minute ledger evidence for object storage.** Usage arrives as
    cumulative provider reports, so it would duplicate them with invented
    per-minute splits.
  - **Loading object-storage pricing in meterd.** It would mean a second
    copy of apid's configuration, and new deploy contract keys.
  - **Three single-rate meters.** Customers budget object storage as one
    line, as it is invoiced.

## Managed PostgreSQL

Managed PostgreSQL is still outside budgets. Its compute-hour and storage
usage has no live priced estimate yet. A later decision will add it as a
meter in the same way.
