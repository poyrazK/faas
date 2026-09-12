# PostgreSQL connection capacity

`FaasPostgresConnectionsHigh` warns when daemon sessions consume more than
75% of PostgreSQL's ordinary-client slots for 10 minutes.
`FaasPostgresConnectionsCritical` pages after five minutes above 90%.
The denominator excludes `superuser_reserved_connections`, so the alert fires
before ordinary daemons consume the operator and recovery reserve.

## Confirm the signal

On the control plane, inspect the exported totals and the timer that produces
them:

```sh
cat /var/lib/node_exporter/textfile_collector/faas_postgres_connections.prom
systemctl status faas-postgres-connection-metrics.timer
journalctl -u faas-postgres-connection-metrics.service -n 50 --no-pager
```

Then group live sessions by their daemon identity and state:

```sql
SELECT coalesce(nullif(application_name, ''), '<unlabelled>') AS application,
       state,
       wait_event_type,
       count(*) AS sessions,
       max(now() - state_change) AS oldest_state
FROM pg_stat_activity
WHERE backend_type = 'client backend'
GROUP BY 1, 2, 3
ORDER BY sessions DESC;
```

The production daemons use `faas-<daemon>` application names. A large
`<unlabelled>` group indicates an old binary, an operator tool, or an
unbudgeted client that needs attribution before its pool can be corrected.

## Recover capacity

1. Find the application whose session count exceeds its documented
   `pkg/db.DaemonMaxConnections` budget.
2. If the sessions belong to a superseded daemon generation, finish or roll
   back that rollout so the old pools close.
3. If a daemon leaked sessions, restart only that daemon after collecting its
   pool and PostgreSQL logs. Confirm the total falls within two collector
   intervals.
4. If active queries account for the pressure, inspect their query age and
   wait event before cancelling work. Preserve rollout, restore, and backup
   evidence for the incident record.

Do not increase `max_connections` as the first response. More backends raise
PostgreSQL memory use and hide a pool-budget regression. Keep the three-node
direct-pool maximum at or below 90 ordinary sessions. If ordinary traffic
cannot stay within that budget during the API, wake, jobs, workflow, and
rollout-overlap load suite, introduce PgBouncer and repeat the capacity test
before expanding the fleet.

## Verify recovery

The warning clears after utilization remains at or below 75%. Verify that new
connections carry an application name and that a rollout overlap cannot push
the exported count beyond the 90-session direct-pool ceiling.
