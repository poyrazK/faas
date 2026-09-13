# FaasStaleDeploymentBacklog

This alert fires when `imaged_stale_deployment_oldest_age_seconds` remains
above 7,200 seconds for ten minutes. The imaged reconciler runs at startup and
every five minutes, cancels at most 64 orphaned rows per pass, and leaves rows
with queued or running builds untouched.

## Verify

Confirm that imaged and its database connection are healthy, then inspect the
candidate rows through the supported operator API:

```sh
systemctl status faas-imaged.service
journalctl -u faas-imaged.service --since '30 minutes ago' --no-pager
gregalectl deployments repair-stale --older-than 2h --limit 200
```

For every row retained by the dry run, inspect its build and stage state:

```sh
gregalectl deployments inspect --deployment-id DEPLOYMENT_ID
```

A queued or running build is owned by builderd. Check builderd health and logs
before interrupting it.

## Recover

An orphaned row has no active build and can be repaired through the audited
command:

```sh
gregalectl deployments repair-stale --older-than 2h --limit 200 --yes
```

The command uses the same CAS-protected cancellation transaction as the daemon,
records the actor and previous state, and never writes directly to PostgreSQL.
After repair, confirm the oldest-age gauge returns to zero and
`imaged_stale_deployments_reconciled_total` increased.

## Escalate

If the gauge remains high after one full five-minute sweep, capture the imaged
and builderd logs, deployment IDs, active build IDs, and the repair command's
trace IDs. Do not mutate deployment or build rows with SQL.
