# MeterdBillingPipeline

Alerts for meterd's billing pipeline (ADR-20261029180000,
ADR-20261030120000). Every minute the sample tick rolls the newest minute
that closed at least 15 s ago, catches up to 15 earlier minutes not yet
recorded (ADR-790), captures price terms and records each minute both the app
and job samplers rolled in full as compute-complete in
`financial_sampling_windows`. Every hour the pusher delivers the hours before
the oldest minute of the 72 h catch-up window that is not recorded complete
(ADR-1162). Every hour the dunning sweep moves past-due accounts on.

A minute that is rolled but not yet recorded is recorded on the next tick
whose price capture and record write succeed, without rolling it again. So
a transient error delays delivery by a minute; only a persistent one stalls it.

## Check

```sh
curl -fsS http://127.0.0.1:9105/metrics | grep -E '^meterd_(usage_final_lag_seconds|ops_total\{op="(sample|stripe|dunning)")'
journalctl -u faas-meterd --since '1 hour ago' --no-pager | grep -E '"meter: (sample|stripe|dunning) tick"|financial pricing|financial sampling window'
```

The oldest minutes of the catch-up window that are not recorded complete
(read-only):

```sql
SELECT g.minute
FROM generate_series(date_trunc('minute', now() - interval '72 hours'),
                     date_trunc('minute', now() - interval '2 minutes'),
                     interval '1 minute') AS g(minute)
LEFT JOIN financial_sampling_windows w ON w.minute = g.minute
WHERE NOT coalesce(w.compute_complete, false)
ORDER BY g.minute
LIMIT 20;
```

## FaasMeterdUsageDeliveryStalled

`meterd_usage_final_lag_seconds` is how far the sample tick is past the oldest
minute not recorded complete. Healthy ticks read 75-135 s. Above 30 minutes,
every account's usage after that minute waits, and cost reports show reduced
coverage.

1. A new environment, or meterd back from an outage of up to three days,
   catches up 15 minutes per tick: the value falls by about 14 minutes each
   minute, so a full window drains in about five hours. Wait.
2. Otherwise `FaasMeterdSampleFailing` usually fires too: follow it.
3. If the sample tick succeeds but the value keeps growing, the record write
   or price capture fails on every tick. Look for `financial pricing` and
   `financial sampling window` errors. One account whose price capture keeps
   failing holds the record for every account (ADR-20261030120000); fix its
   database error rather than waiting.
4. A minute older than 72 h leaves the window and is delivered as it stands.
   Compare the stalled hours with `BillingDrift` once the alert clears.

## FaasMeterdLedgerDrift

The ledger dual run (ADR-20261019193600) compares, per account and UTC month,
the financial ledger's net compute with the compute receipts meterd recorded
as delivered. `drift` means they disagree; `failed` means one account could
not be compared.

1. `meterd_financial_ledger_drift_mb_seconds{month}` is the summed absolute
   difference. Search meterd logs for `ledger reconciliation` to find the
   accounts.
2. A difference right after a plan change, a cap change or a meterd catch-up
   usually means receipts were recorded under different terms than the ledger
   priced; compare the account's `financial_price_snapshots` for the month
   with its `billing_usage_deliveries`.
3. `failed` is a read error: check Postgres, then wait one pass.

## FaasMeterdLedgerEgressDrift

The egress half of the dual run (ADR-20261107105126). For each account and
UTC month the ledger's egress evidence goes, hour by hour, through the rules
meterd bills egress by: the plan in force at the hour and its policy, the
policy's activation hour, Free hours, the subject's egress overage switch
during the hour and `billing_from`. The result is compared with the egress
receipts in `billing_meter_usage_deliveries` (shadow or live). `capped` is a
shortfall the spend cap explains; `drift` is not.

1. `meterd_financial_ledger_egress_drift_bytes{month}` is the summed absolute
   difference. Search meterd logs for `ledger egress drift`: each line has the
   account, the window and both quantities.
2. A shortfall right after an egress overage switch change usually comes from
   receipts settled before the switch was recorded; compare the subject's
   `egress_overage_choices` with the receipts' hours.
3. `incomplete` means egress sampling did not cover the month (meterd's
   network source was missing minutes); it clears only for months sampled
   completely.

## FaasMeterdBillingUsageSourceMismatch

For every hour it delivers, meterd compares the hour's `usage_minutes`
quantity with the financial ledger's (ADR-20261107105126) while
`[meter] billing_usage_source` is `ledger_shadow` (the default) or `ledger`.
The `source` label says which one was billed. Both are written in the same
transaction per sampled minute, so they differ only after an operator ledger
correction (`AppendFinancialAdjustment`) or a bug.

1. Search meterd logs for `billing usage sources disagree`: account, hour and
   both quantities.
2. Look for an adjustment on that account and hour in
   `financial_usage_evidence` (`corrects_source_id` set). A correction is
   expected to bill differently under `ledger`; under `ledger_shadow` the
   customer was billed the uncorrected `usage_minutes`, so decide whether to
   credit the difference.
3. Without a correction, it is a bug: keep `billing_usage_source` where it is
   and do not move to `ledger` until a closed month shows no mismatches.

To change the source, set `[meter] billing_usage_source` in meterd.toml or
`FAAS_BILLING_USAGE_SOURCE` (`off`, `ledger_shadow`, `ledger`) and restart
meterd; an unknown value refuses to start. Every receipt records the source
that billed its hour (`usage_source`, NULL before the column existed).

## FaasMeterdHeldUsageExpiring

meterd holds a pending usage hour back, without settling it, while its billing
subject is `suspended`, `deleting` (hours after the deletion request), has an
incomplete billing identity (`identity_incomplete`, checkout or the
subscription webhook never finished) or is no longer listed
(`unknown_subject`). It retries every pass but only within the billing
lookback (30 days by default); these hours leave it within 72 hours and are
then never billed (ADR-20261103121407).

1. `meterd_billing_usage_held_windows{meter,reason}` is the total held;
   meterd logs `held usage leaves the billing lookback soon` with the counts.
2. `identity_incomplete`: finish or repair the subject's checkout so its
   billing identity has a customer and subscription.
3. `suspended` or `deleting`: decide whether the usage should be billed. To
   bill it, lift the suspension before the hours expire; otherwise the loss is
   accepted and the alert clears when they leave the lookback.

## FaasMeterdQuotaFailing

Three or more quota sweeps failed in 10 minutes. While they fail, Free accounts
are not stopped at their allowance, the egress ceiling is not applied and new
spend-cap holds are not recorded; holds already recorded stay in force. The
tick error (meterd `/healthz` tick detail and logs) names the step:
`quota list accounts`, `load spend caps`, or `account <id> usage`, `spend cap
hold` or `enforce`.

1. `quota list accounts` or `load spend caps`: check Postgres reachability and
   pool headroom (`FaasDBPoolStarved`, `FaasPostgresConnectionsHigh`).
2. One account failing every tick: read its error in the meterd log and fix
   the row; the other accounts are still swept.

## FaasMeterdSampleFailing

Five or more sample ticks failed in 15 minutes. The tick error names the step:
`catch up minute`, `load instance billing residency`, `job sampler`,
`financial pricing` or `financial sampling window`.

1. Check Postgres reachability and pool headroom (`FaasDBPoolStarved`,
   `FaasPostgresConnectionsHigh`).
2. A failing app row (`billable sidecar RAM`, `strict compute funding`,
   `deployment floor`) or `list billing accounts` fails the whole minute; fix
   the cause. The minute is re-rolled once it can succeed, within 72 h.

## FaasMeterdBillingPushFailing

The hourly push to the billing provider returned an error twice in three
hours. The `meter: push usage` warnings carry the account, hour and provider
`code`.

1. `unauthorized` or `forbidden`: the provider token was rotated or revoked.
   Fix `FAAS_POLAR_ACCESS_TOKEN` in meterd's billing secrets and restart meterd.
2. Rate limits or 5xx: the next hourly tick retries every pending hour, and
   the provider ignores repeated events by external ID.
3. `load provider billing identity`: one account's identity row is unreadable;
   the other accounts still deliver.

## FaasMeterdDunningFailing

The dunning sweep failed twice in three hours, so past-due accounts are not
suspended or scheduled for deletion on time and their notices wait. A tick
fails when any account's step fails; the sweep still visits every account.
The `meter: dunning tick` warning carries every failed step with its account
(`dunning suspend <account>`, `dunning schedule deletion <account>`, ...). A
deletion guard refusal is not an error; it is `FaasDunningDeletionBlocked`.
