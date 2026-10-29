# MeterdBillingPipeline

Alerts for meterd's billing pipeline (ADR-20261029180000). Every minute the
sample tick rolls the newest closed minute of usage, catches up to 15 earlier
minutes not yet recorded (ADR-790), captures price terms and records each
minute both the app and job samplers rolled in full as compute-complete in
`financial_sampling_windows`. Every hour the pusher delivers the hours before
the oldest minute of the 24 h catch-up window that is not recorded complete
(ADR-1162). Every hour the dunning sweep moves past-due accounts on.

A minute that is rolled but not yet recorded is recorded on the next tick
whose price capture and record write succeed, without rolling it again. So
a transient error delays delivery by a minute; only a persistent one stalls it.

## Check

```sh
curl -fsS http://127.0.0.1:9105/metrics | grep -E '^meterd_(usage_final_lag_seconds|ops_total\{op="(sample|stripe|dunning)")'
journalctl -u faas-meterd --since '1 hour ago' --no-pager | grep -E '"meter: (sample|stripe|dunning) tick"|financial pricing|financial sampling window'
```

The oldest minutes of the last day that are not recorded complete (read-only):

```sql
SELECT g.minute
FROM generate_series(date_trunc('minute', now() - interval '24 hours'),
                     date_trunc('minute', now() - interval '2 minutes'),
                     interval '1 minute') AS g(minute)
LEFT JOIN financial_sampling_windows w ON w.minute = g.minute
WHERE NOT coalesce(w.compute_complete, false)
ORDER BY g.minute
LIMIT 20;
```

## FaasMeterdUsageDeliveryStalled

`meterd_usage_final_lag_seconds` is how far the sample tick is past the oldest
minute not recorded complete. Healthy ticks read 60-120 s. Above 30 minutes,
every account's usage after that minute waits, and cost reports show reduced
coverage.

1. A new environment, or meterd back from an outage of hours, catches up 15
   minutes per tick: the value falls by about 14 minutes each minute. Wait.
2. Otherwise `FaasMeterdSampleFailing` usually fires too: follow it.
3. If the sample tick succeeds but the value keeps growing, the record write
   or price capture fails on every tick. Look for `financial pricing` and
   `financial sampling window` errors; an account with an unknown plan fails
   price capture for every account.
4. A minute older than 24 h leaves the window and is delivered as it stands.
   Compare the stalled hours with `BillingDrift` once the alert clears.

## FaasMeterdSampleFailing

Five or more sample ticks failed in 15 minutes. The tick error names the step:
`catch up minute`, `load instance billing residency`, `job sampler`,
`financial pricing` or `financial sampling window`.

1. Check Postgres reachability and pool headroom (`FaasDBPoolStarved`,
   `FaasPostgresConnectionsHigh`).
2. A failing app row (`billable sidecar RAM`, `strict compute funding`) fails
   the whole minute; fix the row's cause. The minute is re-rolled once it can
   succeed, within 24 h.

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
suspended or scheduled for deletion on time and their notices wait. The
`meter: dunning tick` warning carries the error. A deletion guard refusal is
not an error; it is `FaasDunningDeletionBlocked`.
