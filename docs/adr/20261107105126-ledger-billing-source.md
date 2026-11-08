# ADR 20261107105126 · The financial ledger becomes the billing source, behind a switch

- **Status:** accepted
- **Date:** 2026-11-07
- **Amends:** ADR-20261019193600 (ledger dual run: egress joins, gate to the
  switch), ADR-20261101143527 (Free hours are not billed compute anywhere),
  ADR-20261103131742 amendment 2 (the spend cap counts only billed egress)
- **Related:** ADR-566 (financial visibility), ADR-1162 (usage delivery and
  spend cap), ADR-20261031100000 and ADR-20261031161842 (one billed-compute
  figure)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

Billing GA step 3. meterd writes each sampled minute twice, in one
transaction: a `usage_minutes` row and the financial ledger's evidence
(ADR-566). The provider pusher bills `usage_minutes`; customers' cost
reports, budgets and the operator views read the ledger. The dual run
(ADR-20261019193600) compared the two for compute once a month. Making the
ledger the billing source needs evidence that both agree hour by hour and a
way back if they do not.

The owner chose the evidence on 2026-11-07: a replay of a closed month that
must match exactly, then a Polar sandbox month billed from the ledger before
production moves. Scope: compute and egress. Object storage and managed
PostgreSQL keep their own month-close paths.

The replay (`pkg/meter/ledger_replay_test.go`: eight paid subjects through
plan changes, a late subscription, an organization, the egress overage
switch, a spend cap and a month boundary, on the memory store and on
Postgres, every subject's delivery checked by hand) found three faults:

1. **Free hours counted as billed compute.** `state.MonthBilledComputeMBSeconds`
   and the ledger's billed grant priced a Free segment's usage above Free's
   allowance as overage. The pusher never bills a Free hour
   (ADR-20261101143527), so a subject that subscribed mid-month after using
   Free had that phantom overage spend its cap, show on its usage pages and
   drift in the dual run, as ADR-20261101143527 predicted.
2. **Unbilled egress spent the cap.** The pusher's compute and egress clamps
   and meterd's cap hold reserved all egress above the allowance, also bytes
   sent while the subject's overage switch was off, which are never billed
   (ADR-20261103131742 amendment 2). A capped subject near its cap was billed
   less compute than its cap allowed.
3. **The memory store refused organizations** in financial compute
   transactions, so its organizations' egress was never delivered in tests.
   Postgres accepts them.

## Decision

1. **A usage source switch.** `[meter] billing_usage_source` in meterd.toml,
   overridden by `FAAS_BILLING_USAGE_SOURCE`, is `off`, `ledger_shadow` (the
   default) or `ledger`; any other value refuses to start. It decides what
   the pusher quantifies billed hours from: each pending window's quantity,
   the month-to-date sums behind the overage cursors, and the month figures
   the spend-cap clamps reserve, for compute and egress. The billing rules do
   not change: the plan in force at each hour, Free hours, `billing_from`,
   the egress policy and overage switch, the spend cap.
   - `off` bills `usage_minutes` and compares nothing.
   - `ledger_shadow` bills `usage_minutes` and compares every delivered
     hour's quantity with the ledger's.
   - `ledger` bills the ledger and compares every delivered hour with
     `usage_minutes`. A ledger that cannot be read holds the hour; in
     shadow mode it is only logged.

   Each comparison counts `meterd_billing_usage_source_compared_windows_total`
   and, on a difference, `meterd_billing_usage_source_mismatch_windows_total`
   `{meter, source}` with a warning naming the account, hour and both
   quantities. `FaasMeterdBillingUsageSourceMismatch` fires for an hour after
   any mismatch. An operator correction of the ledger
   (`AppendFinancialAdjustment`) is the only expected difference: under
   `ledger` the corrected usage is billed.

   meterd's cap hold, schedd's cap gate and the usage pages keep reading
   `usage_minutes`; while the comparison finds no mismatch they read the same
   numbers.
2. **Receipts record their source.** `billing_usage_deliveries` and
   `billing_meter_usage_deliveries` gain `usage_source` (`usage_minutes` or
   `ledger`; NULL for receipts written before the column). The first receipt
   for an hour wins, source included.
3. **Egress joins the dual run.** The ledger's egress evidence, by hour, goes
   through the egress rules and is compared with the egress receipts (shadow
   or live), with `meterd_financial_ledger_egress_reconcile_accounts{month,
   result}`, `meterd_financial_ledger_egress_drift_bytes{month}` and
   `FaasMeterdLedgerEgressDrift`. A shortfall is `capped` only when the spend
   cap, clamped hour by hour at the month's last allowance, explains it.
4. **One egress implementation.** `billEgressHours` applies the egress hour
   rules for the pusher's cap clamps, meterd's cap hold and the dual run, so
   egress the subject is not billed for (overage off, Free hours) never
   spends the cap. `billing_from` marks where the current provider's delivery
   starts; a paid hour before it was billed by the provider it replaced and
   still spends the cap, as compute's does.
5. **Free hours are not billed compute anywhere.**
   `MonthBilledComputeMBSeconds` and the ledger's billed grant skip Free
   segments, as the pusher does.
6. **The gate to `ledger`.** Production moves from `ledger_shadow` to
   `ledger` only after all three:
   1. the replay passes in every mode on both stores (CI);
   2. one closed production month under `ledger_shadow` with no mismatched
      window and no compute or egress drift in the dual run;
   3. a Polar sandbox month (2026-12-01 to 2026-12-31) billed under `ledger`
      after a real card checkout, whose Polar meter totals equal the
      receipts.

## Consequences

- The ledger can bill without a migration of billing history: receipts say
  which source billed each hour, and switching back is a config change.
- `ledger_shadow` and `ledger` read the ledger once per delivered window: one
  indexed query per account-hour per pass.
- The cap hold counts egress by the hour rules: bytes sent while overage was
  off no longer count after it was turned on earlier in the month
  (`TestRunQuotaOnce_EgressWithOverageOffDoesNotCountTowardCap` changed).
- Usage pages show no overage for Free hours, and a subscriber's first month
  no longer starts with part of its cap spent.
- Limits kept: the dual run still skips subjects that are Free now, and a
  cancelled subscription's last undelivered paid hours stay as
  ADR-20261031161842 accepted.
