# FaasHostClockUnsynchronized / FaasHostClockSkew

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml` (group `faas_host_resources`).
Metrics: `node_timex_sync_status`, `node_timex_offset_seconds`
(node_exporter). Severity: warn.

## Symptom

- `FaasHostClockUnsynchronized`: the kernel has reported an unsynchronized
  clock for 15 minutes.
- `FaasHostClockSkew`: the time daemon has reported an offset above
  500 ms for 10 minutes.

Time matters more than usual on Gregale hosts. Restored guests step their
clock from the host in the post-restore resume hook, so a wrong host clock
propagates into every woken app. Internal mTLS certificates, signed tokens
and lease deadlines also assume hosts agree on time.

## Check

```bash
timedatectl status
timedatectl timesync-status 2>/dev/null || chronyc tracking
systemctl status systemd-timesyncd chrony 2>/dev/null | head -20
```

Cloud hosts usually sync from the provider's metadata time source (GCP:
`metadata.google.internal`); confirm it is reachable from the host.

## Recover

- Restart the time daemon: `sudo systemctl restart systemd-timesyncd`
  (or `chrony`), then watch `timedatectl` until
  `System clock synchronized: yes`.
- If the offset is large, step once deliberately rather than waiting for
  slew: `sudo chronyc makestep` (chrony) or restart timesyncd.
- After a large correction on a compute host, recently restored guests may
  have inherited the wrong time; park and re-wake the affected apps.

## Silence

```bash
amtool --alertmanager.url=http://127.0.0.1:9094 silence add \
  'alertname=~"FaasHostClock.*"' instance="<instance>" \
  --duration=1h --comment='time sync repair in progress'
```
