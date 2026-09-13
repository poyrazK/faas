# FaasComputeHeartbeatRetentionStalled

Schedd keeps seven days of raw `compute_node_heartbeats`. Before deleting an
expired row it folds the sample into `compute_node_heartbeat_hourly`, which is
the durable capacity-history source. Each transaction claims at most 5,000 raw
rows with `FOR UPDATE SKIP LOCKED`; one pass commits at most eight batches.

The worker runs immediately at process start and hourly afterward. It exports:

- `schedd_compute_heartbeat_retention_last_success_timestamp_seconds`
- `schedd_compute_heartbeat_retention_oldest_raw_age_seconds`
- `schedd_compute_heartbeat_retention_rows_deleted_total`
- `schedd_compute_heartbeat_retention_rollup_buckets_total`
- `schedd_compute_heartbeat_retention_failures_total`

When the stalled alert fires, first confirm the writer and maintenance worker:

```sh
systemctl status faas-schedd
journalctl -u faas-schedd --since '3 hours ago' | grep 'compute heartbeat retention'
```

Inspect the raw backlog and the hourly record without selecting payload data:

```sql
select count(*) as raw_rows, min(received_at) as oldest_raw
from compute_node_heartbeats;

select count(*) as hourly_buckets, min(bucket_at) as oldest_bucket,
       max(bucket_at) as newest_bucket
from compute_node_heartbeat_hourly;
```

Check for a long transaction that prevents cleanup, then repair database
connectivity or cancel only the confirmed blocking transaction. Restarting
schedd is safe and triggers an immediate bounded pass. Do not run a standalone
`DELETE`: it bypasses the hourly rollup and destroys capacity history.

For an existing backlog, leave the service running and watch the deleted
counter increase after each pass. At ten nodes and the normal 30-second writer
cadence, the worker's 40,000-row pass budget exceeds a full day of new rows, so
the backlog converges while inserts and latest-per-node reads remain available.
The backlog warning clears when the oldest raw age falls below eight days.

Verify recovery:

```promql
time() - schedd_compute_heartbeat_retention_last_success_timestamp_seconds
schedd_compute_heartbeat_retention_oldest_raw_age_seconds
increase(schedd_compute_heartbeat_retention_failures_total[1h])
increase(schedd_compute_heartbeat_retention_rows_deleted_total[1h])
```

The first value must be below 7,200 seconds, failures must stop increasing, and
hourly bucket counts must remain nonzero for periods whose raw rows were
deleted.
