# FaasSnapshotDriftAuditUnavailable

## Symptom

The hourly snapshot inventory did not complete, so `snapshot_disk_drift_total` cannot be interpreted as a clean result.

## Check

1. Check `schedd_snapshot_disk_drift_duration_seconds`, `schedd_snapshot_disk_drift_objects_processed`, and schedd logs for OCI authentication, repository listing, or timeout errors.
2. Confirm GHCR/OCI credentials and registry reachability from the schedd host.
3. Compare inventory size with the two-minute remote sweep budget. A growing inventory should complete within that budget; partial enumeration must remain a failure.

## Recover

After repair, run or wait for one sweep and confirm the last-success timestamp advances and consecutive failures reset to zero.
