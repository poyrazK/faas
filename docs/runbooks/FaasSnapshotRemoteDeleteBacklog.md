# FaasSnapshotRemoteDeleteBacklog

## Symptom

These alerts mean snapshot GC could not verify deletion of memory or VM-state
artifacts in the remote OCI registry. Imaged marks the snapshot unusable first,
keeps a `delete_pending` database tombstone, and retries cleanup on each normal
GC tick. An ordinary stale snapshot kept for rollback does not enter this
backlog.

## Triage

Confirm the backlog and recent failures on imaged's metrics endpoint:

```sh
curl -fsS http://127.0.0.1:9102/metrics \
  | grep -E '^imaged_snapshot_remote_delete_(backlog|failures)'
journalctl -u faas-imaged --since '1 hour ago' --no-pager \
  | grep -E 'remote delete backlog|gc remove snap|registry delete unsupported'
```

Inspect only the durable deletion queue. Rows that have `stale=true` and
`delete_pending=false` are rollback-retention records and must remain intact.

```sql
select id, deployment_id, tier, storage_key, created_at
from snapshots
where delete_pending = true
order by created_at;
```

For GHCR, run the same non-destructive preflight used by `cd-compute`:

```sh
sudo -u faas-imaged gregalectl artifact lifecycle-check \
  --env-file /etc/faas/storage.env \
  --lifecycle-env-file /etc/faas/imaged-storage.env --json
```

The command writes, reads, and deletes a tiny disposable artifact without
printing credentials. Verify that the lifecycle token has package read,
write, and delete scope and that the configured username owns the package
namespace. A 401 or 403
indicates credentials or package ownership; a distribution API 405 followed by
a GitHub Packages API failure indicates the fallback could not complete.

## Recover

Restore registry credentials or reachability, then wait for or restart the
normal imaged GC timer. Successful retries delete both remote tags before
removing the database row. Confirm that the backlog reaches zero and the
package version is absent from the registry.

Do not delete `delete_pending` rows manually. Removing the row first loses the
durable key needed to retry the remote cleanup and leaves billed registry data
orphaned.
