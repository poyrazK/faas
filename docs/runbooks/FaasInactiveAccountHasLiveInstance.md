# FaasInactiveAccountHasLiveInstance

This alert fires when schedd reconciles a live or waking instance whose account
is suspended or deletion-pending. The scheduler drains the instance in the same
path, but any occurrence means an admission or lifecycle notification raced or
failed and must be investigated.

## Symptom

`schedd_account_lifecycle_violations_total` increased because a live instance
was observed after its account became inactive. Customer requests are rejected,
but the VM can consume capacity until reconciliation finishes.

## Check

Check the current invariant in PostgreSQL:

```sql
SELECT i.id, i.state, i.node_id, a.id AS app_id, a.slug, ac.id AS account_id, ac.status
FROM instances i
JOIN apps a ON a.id = i.app_id
JOIN accounts ac ON ac.id = a.account_id
WHERE ac.status IN ('suspended', 'deleted_pending')
  AND i.state IN ('waking', 'cold_booting', 'running', 'snapshotting');
```

If rows remain, confirm the owning `faas-schedd` service is healthy and inspect
its logs for `park app`, `lifecycle reconciliation`, and VM destroy failures.

## Recover

Do not reactivate the account as a repair. Restart schedd only after preserving
the relevant wake IDs and errors; its reaper will retry reconciliation.

Then identify the producer that admitted the instance and verify the gateway,
cron, job, async, floor, prewarm, and scale-up paths all reached the shared
account lifecycle gate. The alert clears after the ten-minute increase window
contains no new violations.
