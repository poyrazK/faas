# FaasDunningDeletionBlocked

This alert fires when meterd's dunning sweep has been unable, for at least two
consecutive hourly sweeps, to move suspended accounts past the non-payment
deadline (21 days after `past_due_at`) into `deleted_pending`. A deletion guard
in the database refuses the transition:

- `managed_postgres_databases`: the account still owns live managed PostgreSQL
  databases (trigger `guard_account_managed_postgres_databases`).
- `shared_app_ownership`: company apps still reference the account through a
  legacy storage or billing link.
- `unknown`: apid reported a guard reason this meterd does not know; upgrade
  meterd.

## Symptom

`meterd_dunning_deletion_blocked_accounts{reason}` is the number of accounts the
last sweep could not advance. These accounts stay suspended indefinitely: no
deletion notice is sent, the 30-day erasure grace never starts, and their
provider databases keep running at platform cost. Nothing is retried faster
than the hourly sweep, and nothing is deleted automatically.

## Check

meterd logs each blocked account once per sweep:

```sh
journalctl -u faas-meterd --since "2 hours ago" | grep 'non-payment deletion blocked'
```

The line carries `account`, `reason` and, for `managed_postgres_databases`, the
number of live databases. When the durable non-payment command is enabled, apid
logs `apid: non-payment deletion blocked` with the same fields.

To list the suspended accounts past the deadline that still own live personal
databases:

```sql
SELECT a.id, a.past_due_at, count(d.id) AS live_databases
FROM accounts a
JOIN managed_postgres_databases d ON d.account_id = a.id AND d.state <> 'deleted'
JOIN orgs o ON o.id = d.org_id AND (o.personal_org OR NOT o.resource_ownership_enabled)
WHERE a.status = 'suspended'
  AND a.past_due_at < now() - interval '21 days'
GROUP BY a.id, a.past_due_at
ORDER BY a.past_due_at;
```

## Recover

Do not delete `managed_postgres_databases` rows or provider projects by hand,
and do not disable the guard trigger: the guard is what stops a deletion from
leaving a running database without an owner.

- `managed_postgres_databases`: the databases must reach the provider-confirmed
  `deleted` tombstone through the managed PostgreSQL lifecycle. Contact the
  customer, or follow the account's support process, so the databases are
  deleted (`gregale postgres delete`). The next hourly sweep then moves the
  account to `deleted_pending` and the gauge drops to zero.
- `shared_app_ownership`: complete the company app ownership transfer for the
  account; the next sweep proceeds once the guard clears.

The alert clears after the first sweep that finds no blocked account.
