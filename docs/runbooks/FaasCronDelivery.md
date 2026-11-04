# FaasCronDelivery

Two warnings share this runbook. Both cover HTTP crons, which schedd queues
as invocations for the invocation drain to deliver
([ADR-20261026201726](../adr/20261026201726-cron-delivery-through-the-drain.md)).
Scheduled fires and manual runs (`gregale crons run`) go through the same path.

- **FaasCronDeliveryLag**: the p99 delay between a run's scheduled minute
  (or a manual run's request) and its first delivery claim has been above 60
  seconds for ten minutes.
- **FaasCronDeliveryFailing**: across the fleet, more than half of at least 20
  deliveries in 30 minutes have ended `failed` or `dead_letter`, for thirty
  minutes.

Command crons have their own alerts (`faas_command_cron_admission`).

## Confirm the signal

```promql
histogram_quantile(0.99,
  sum by (le) (rate(schedd_cron_schedule_lag_seconds_bucket[10m])))
```

```promql
sum by (outcome) (rate(schedd_cron_delivery_total[30m]))
```

A plain HTTP cron makes one delivery attempt per scheduled time, so a failure
shows up as `failed`. `retry` and `dead_letter` come only from crons with
failure rules. Compare with `schedd_delayed_task_dispatch_total` and the
invocation backlog: if every async source is late or failing, the problem is
the drain or the database, not crons.

## Diagnose

1. Check that every schedd instance is ready and that its main loop is
   ticking. The cron tick runs once a minute; loop-stall alerts and recent
   restarts point at the scheduler itself.
2. Inspect durable invocation depth, oldest age and account concurrency-cap
   pressure. Cron runs share the drain with other background work, so a broad
   backlog delays them too.
3. For failures, read schedd's `drain: wake` and `drain: invoke` warnings. A
   burst of wake errors points at compute capacity or snapshot restore; a
   burst of invoke errors points at gatewayd-internal or routing.
4. Check whether the failures follow a deploy of schedd, gatewayd or vmmd.
5. Confirm clock synchronization on control-plane hosts; skew distorts both
   due selection and the lag measurement.

## Remediate

- Restore unhealthy schedd, gateway or database instances first.
- Add scheduler or compute capacity when lag tracks a sustained backlog.
- Roll back a release that coincides with the failures.

Do not re-fire crons by hand to make up for late or failed runs. Customers'
handlers may not be idempotent, and the next scheduled time is the documented
retry for a plain HTTP cron.

## Resolution

The lag alert clears once the rolling p99 stays at or below 60 seconds; the
failure alert clears once failed and dead-lettered deliveries fall to half or
less of the total. Before closing, confirm the invocation backlog is falling.
