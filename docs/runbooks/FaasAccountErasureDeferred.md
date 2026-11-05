# FaasAccountErasureDeferred

## Meaning

`apid_account_grace_deferred_accounts` has been above zero for a day. Those
accounts are past their 30-day deletion grace, but apid's grace sweep keeps
their rows because a cleanup step that must run first keeps failing:

- object buckets (`CleanupExpiredAccountObjectBuckets`);
- archived runtime logs (`log archive: ...`, see
  [FaasLogArchiveShipperDegraded](FaasLogArchiveShipperDegraded.md#account-erasure));
- managed PostgreSQL databases (`managed postgres: ...`);
- hosting sites (`hosting site cleanup deferred`): the sweep starts deleting
  every active site, and the hosting reconciler purges each one after its
  routes and releases retire;
- the final delete itself (`grace: hard delete failed`). It refuses, by
  reason, while rows another workflow owns remain (`account purge waits for N
  hosting_site | environment_clone | managed_postgres_fence row(s)`), and it
  counts toward the gauge (`apid_app_grace_failures_total{stage="account_purge_<reason>"}`).
  Any other error there, such as a foreign key violation, is a purge defect:
  the account purge needs a step for that table, and
  `pkg/state/purgeguard` should have caught it.

The account stays in `deleted_pending` and nothing else is lost, but the DPA
(section 11) promises erasure within 30 days of the grace ending.

## Diagnose

apid logs one line per held account and tick, with the failing step:

```bash
journalctl -u faas-apid --since '-1h' | grep 'grace: external cleanup deferred'
```

- `log archive: account logs are not erased yet: list ...: 403` — apid's archive
  identity cannot list or delete in the archive bucket. Grant
  `storage.objects.list` and `storage.objects.delete` (GCS) or
  `s3:ListBucket` and `s3:DeleteObject` on `faas-logs/*`.
- `log archive: ... more remain` — normal progress on an account with many
  instances (200 per tick). It clears on its own; check it is moving:

  ```sql
  SELECT account_id, count(*) FILTER (WHERE erased_at IS NULL) AS pending
  FROM log_archive_erasure_instances GROUP BY account_id;
  ```

- `managed postgres: ...` — see
  [the managed PostgreSQL runbook](../managed-postgres-preview-runbook.md).

## Recover

Fix the failing step; the next tick (60 s) retries and deletes the account
once every step succeeds. Never delete the account row by hand: the cleanup
steps find the account's resources through it.
