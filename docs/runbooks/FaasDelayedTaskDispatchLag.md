# FaasDelayedTaskDispatchLag

This warning means the p99 delayed-task first-claim lag has remained above 60
seconds for ten minutes. Lag is measured from the customer-requested execution
time to the scheduler's first successful claim. Retries do not add another lag
sample.

## Confirm the signal

Check the fleet-wide p99 and dispatch outcomes:

```promql
histogram_quantile(0.99,
  sum by (le) (rate(schedd_delayed_task_schedule_lag_seconds_bucket[10m])))
```

```promql
sum by (outcome) (rate(schedd_delayed_task_dispatch_total[10m]))
```

A rising `retry`, `failed`, or `dead_letter` rate points to target wake or
invoke failures. High lag with normal outcome rates points to scheduler,
database, or concurrency pressure before dispatch.

## Diagnose

1. Check that every schedd instance is ready and that its drain loop is making
   progress. Review recent restarts and loop-stall alerts.
2. Inspect durable invocation depth, oldest age, and account concurrency-cap
   pressure. A broad backlog indicates insufficient scheduler or compute
   capacity; one account can be limited by its plan concurrency.
3. Check Postgres latency and connection saturation. Claims and terminal state
   transitions are durable database operations.
4. Compare the outcome counter rates. For retries or failures, inspect schedd
   logs for wake and invoke errors and verify the target app is healthy.
5. Confirm clock synchronization on control-plane hosts. Significant clock
   skew can distort both due selection and the lag measurement.

## Remediate

- Restore unhealthy schedd or database instances before changing customer
  tasks.
- Add scheduler/compute capacity when lag correlates with a sustained backlog.
- Correct target-app failures before replaying dead-lettered work.
- If an account is legitimately at its concurrency limit, let active work
  drain or raise the plan limit through the normal quota process.

Do not cancel and recreate delayed tasks merely to clear the alert. Accepted
tasks remain durable and are dispatched when capacity recovers; recreating them
can duplicate side effects unless the producer uses a stable idempotency key.

## Resolution

The alert clears after the rolling p99 remains at or below 60 seconds. Before
closing the incident, confirm the pending backlog is falling and terminal
failure/dead-letter rates are normal.
