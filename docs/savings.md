# Scale-to-zero savings

Gregale parks an idle app and wakes it on the next request, so you pay for
RAM only while the app is running. The savings estimate shows how much that
saved compared with keeping the app running all the time.

## Read the estimate

```sh
curl -H "Authorization: Bearer $GREGALE_TOKEN" \
  "https://api.gregale.dev/v1/apps/my-api/savings"
```

The default window is the trailing 30 days, ending at today's UTC midnight.
Pass `since` and `until` as RFC3339 timestamps to choose another window. Both
snap to UTC midnight, and `since` never goes further back than 30 days before
`until`, because detailed usage is kept for 30 days.

The estimate is available on Hobby and above. The Free plan gets
`402 plan_app_usage_summary_not_allowed`, the same as the per-app usage
summary.

## How it is calculated

| Figure | Meaning |
|---|---|
| `actual_*` | RAM-time the app was billed for in the window (the same number as `GET /v1/apps/{slug}/usage`). |
| `always_on_*` | RAM-time of `baseline_instances` instances of `billable_ram_mb`, running from `baseline_start` to the end of the window. |
| `saved_*` | `always_on - actual`, never below zero. |
| `parked_ratio` | Share of the always-on RAM-time the app did not use. |

- `billable_ram_mb` is the app's RAM plus the 8 MB per-VM overhead that
  billing uses.
- `baseline_instances` is the app's `min_instances`, or 1 when it scales to
  zero.
- `baseline_start` is the app's first billed hour in the window, so a new app
  is not credited for time before it first ran. An app that billed nothing in
  the window shows no savings.
- Money is in integer millicents at the plan overage rate
  (`price_millicents_per_gb_hour`, €0.01 per GB-hour today).

The figure is a conservative estimate, not an invoice line:

- Companion sidecars are billed in `actual_*` but left out of the always-on
  baseline, which can only lower the saving.
- A burst that ran more instances than the baseline can make actual usage
  exceed it. The saving then shows zero rather than a negative number.
- Hours inside your plan's included GB-hours are valued at the overage rate
  too, so the money figure shows what the RAM-time is worth, not what your
  invoice would have changed by.
