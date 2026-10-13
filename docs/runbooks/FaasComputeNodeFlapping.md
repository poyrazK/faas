# FaasComputeNodeFlapping / FaasInstancesRecreatedByRecovery

Schedd's heartbeat pings each compute node's vmmd. After
`DefaultHeartbeatFailureThreshold` (3) consecutive failed pings it marks the
node `unavailable` and emits `node.failed`. The recovery arbiter then parks
the node's running instances with `kind=recovery_recreate` as if their VMs
had died with the node, and their next request wakes them elsewhere.

- `schedd_recovery_event_emitted_total{kind="failed"}` counts node failures.
- `schedd_recreate_decisions_total{outcome="succeeded"}` counts parked
  instances.

On production-us (2026-10-12, hunt #9, H9-171) a starved control plane timed
pings out while both nodes were healthy. They flapped about 22 times each in
36 minutes, and every flap left the parked instances' VMs running: 53 VMs
(5.5 GB) were found behind parked rows. ADR-191's 2026-10-12 amendment covers
the safeguards: the failure threshold, schedd destroying the VMs it parked
once the node answers, and vmmd's sweep of VMs whose row is not live.

## Symptom

`FaasComputeNodeFlapping` (page): at least three node failures in 30 minutes.
`FaasInstancesRecreatedByRecovery` (warn): the arbiter parked running
instances in the last 15 minutes.

## Check

Is the node actually down, or is the prober slow? A prober-side cause shows up
as `deadline exceeded while waiting for conn` on several nodes at once:

```sh
journalctl -t schedd --since '1 hour ago' | grep -E 'ping failed|answered again|recreate: instance transitioned'
uptime; free -m
```

Recent failures and recoveries:

```sql
select at, kind, data from events
where kind in ('node.failed', 'node.recovered') and at > now() - interval '2 hours'
order by at;
```

On each compute node, compare running VMs with live rows. Any VM whose row is
not live is an orphan:

```sh
sudo ps -eo args | grep '^/firecracker' | awk '{for(i=1;i<=NF;i++) if($i=="--id") print $(i+1)}'
```

```sql
select id, state from instances where id in (<ids from the node>);
```

## Act

- Prober-side stall (control-plane CPU, memory, I/O or Postgres): relieve the
  control plane first. Failovers stop once pings answer within the probe
  timeout.
- Node genuinely down: let the failover proceed. Parked apps wake on other
  nodes; worker pools are reconciled automatically.
- Orphaned VMs: schedd destroys the VMs it parked once the node answers, and
  vmmd's sweep destroys any app VM whose row stays non-live for two
  consecutive minutes. If orphans persist past a few minutes, check
  `journalctl -t vmmd | grep 'live VM reap'` and
  `journalctl -t schedd | grep 'lost VM'` for destroy errors.
