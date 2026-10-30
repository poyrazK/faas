# FaasAppDeletionGraceDegraded

Apid permanently removes an app only after its seven-day restore deadline and
after every exclusively owned rootfs, sidecar, snapshot, vmstate, and SBOM
object has been deleted. Database references remain available for retry when
storage cleanup fails.

## Symptom

`FaasAppDeletionDeadlineMissing` means a tombstone has no purge deadline and is
quarantined. `FaasAppDeletionArtifactsRetained` means an expired tombstone
still owns physical artifact bytes after repeated sweeps.
`FaasAppDeletionPurgeBlocked` means the final database delete of an expired
app was refused, by the `reason` label:

- `foreign_key`: a row the purge does not remove still references the app.
  That is a purge defect: the app needs a purge step (or an `ON DELETE`
  rule) for that table. The sweep backs off per app, doubling from 2 minutes
  to 6 hours, because each failed attempt locks every table the purge and its
  cascades touch; retried every minute, that deadlocked a migration adding a
  foreign key to `apps` on 2026-10-24. An apid restart retries at once.
- `managed_postgres_binding`: a binding that is not a deleted tombstone holds
  a deleted app. Deleting an app is refused while it has a live binding, so
  this should not happen. Deleted binding tombstones are removed by the purge
  itself.

## Check

Inspect the gauges and failure stage without exposing customer object keys:

```promql
apid_app_grace_missing_deadlines
apid_app_grace_expired_artifact_bytes
apid_app_grace_blocked_apps
increase(apid_app_grace_failures_total[1h])
time() - apid_app_grace_last_success_timestamp_seconds
```

For a blocked purge, the apid log names the app, and for a foreign key the
constraint and the referencing table:

```sh
journalctl -u faas-apid --since '6 hours ago' | grep -E 'app purge blocked|waits for retained resources'
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
objects. A normal apid sweep retries every expired tombstone each minute,
except one blocked by a foreign key, which waits out its back-off.

For `foreign_key`, ship a purge step for the named table (in
`DeleteAppPermanently`) rather than deleting the referencing rows by hand; the
release restarts apid, which clears the back-off. For
`managed_postgres_binding`, release the binding through the binding service
(`DELETE /v1/postgres/bindings/{id}`), never by deleting or cascading the row:
the service revokes the provider credential before it writes the tombstone
(ADR-155), and the next sweep purges the app.

For a missing deadline written after the backfill migration, preserve the row
for investigation, identify the writer that bypassed the trigger, and set a
fresh seven-day deadline. This restores the customer's full recovery window.
The alerts clear when the gauges reach zero and the last-success timestamp
continues advancing.
