# FaasAppDeletionGraceDegraded

Apid permanently removes an app only after its seven-day restore deadline and
after every exclusively owned rootfs, sidecar, snapshot, vmstate, and SBOM
object has been deleted. Database references remain available for retry when
storage cleanup fails.

## Symptom

`FaasAppDeletionDeadlineMissing` means a tombstone has no purge deadline and is
quarantined. `FaasAppDeletionArtifactsRetained` means an expired tombstone
still owns physical artifact bytes after repeated sweeps.

## Check

Inspect the two gauges and failure stage without exposing customer object keys:

```promql
apid_app_grace_missing_deadlines
apid_app_grace_expired_artifact_bytes
increase(apid_app_grace_failures_total[1h])
time() - apid_app_grace_last_success_timestamp_seconds
```

Confirm the migration and the background worker are active:

```sh
systemctl status faas-apid
journalctl -u faas-apid --since '2 hours ago' | grep 'grace:'
```

Count affected rows and projected bytes in PostgreSQL. Do not print storage
keys into an incident channel because app slugs may be customer-identifying.

## Recover

Repair StorageBackend connectivity or permissions first. Do not delete the app
rows manually: that discards the durable retry inventory and leaves orphaned
objects. A normal apid sweep retries every expired tombstone each minute.

For a missing deadline written after the backfill migration, preserve the row
for investigation, identify the writer that bypassed the trigger, and set a
fresh seven-day deadline. This restores the customer's full recovery window.
The alerts clear when both gauges reach zero and the last-success timestamp
continues advancing.
