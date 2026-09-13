# Request telemetry partitions degraded

`meterd` keeps explicit monthly `request_telemetry` partitions attached for the
current month and the next two months. It runs the reconciliation once during
startup and then hourly. The default partition is a safety net; current traffic
continuing to land there means explicit coverage is incomplete.

## Alerts

- `FaasRequestTelemetryPartitionCoverageMissing` fires when fewer than three
  expected partitions are attached or the current month is missing.
- `FaasRequestTelemetryPartitionReconcileStalled` fires when startup
  reconciliation has not succeeded within ten minutes, or the last success is
  more than two hours old.
- `FaasRequestTelemetryDefaultReceivingRows` fires when the default partition
  received a row in the last ten minutes.

## Triage

1. Check the reconciliation metrics and recent `meterd` errors:

   ```promql
   meterd_request_telemetry_partition_covered_months
   meterd_request_telemetry_partition_current_covered
   time() - meterd_request_telemetry_partition_last_success_timestamp_seconds
   increase(meterd_request_telemetry_partition_reconcile_failures_total[2h])
   meterd_request_telemetry_default_rows
   meterd_request_telemetry_default_latest_received_timestamp_seconds
   ```

2. Inspect attached partitions and their bounds:

   ```sql
   SELECT child.relname,
          pg_get_expr(child.relpartbound, child.oid) AS bounds
     FROM pg_inherits inheritance
     JOIN pg_class parent ON parent.oid = inheritance.inhparent
     JOIN pg_class child ON child.oid = inheritance.inhrelid
    WHERE parent.oid = 'public.request_telemetry'::regclass
    ORDER BY child.relname;
   ```

3. Inspect rows currently in the default partition. A timestamp in the current
   month points to missing partition coverage; a timestamp far in the future or
   past points to a producer clock or timestamp-validation problem.

   ```sql
   SELECT min(received_at), max(received_at), count(*)
     FROM public.request_telemetry_default;
   ```

4. Check `meterd` logs for `request telemetry partition reconciliation failed`.
   Lock timeouts mean another DDL operation or long transaction is blocking the
   short reconciliation transaction. A relation-name conflict means an
   unattached table already uses the expected `request_telemetry_YYYYMM` name;
   inspect that table before renaming or removing it.

Restarting a healthy `meterd` triggers an immediate retry. Do not manually move
or delete default rows while the daemon is active: the reconciler takes an
exclusive parent lock and moves rows transactionally before attaching a missing
partition.

## Retention invariant

Row-level retention remains plan-specific. Monthly explicit partitions are
dropped only after the partition's upper bound is at least 14 days old, which
preserves the longest Scale-plan retention window. Confirm that invariant before
changing the retention cutoff.
