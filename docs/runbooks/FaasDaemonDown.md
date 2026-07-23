# FaasDaemonDown

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `up{job=~"apid|gatewayd|schedd|vmmd|imaged|builderd|meterd|githubd"}`.
Severity: page.

## Symptom

Prometheus has been unable to scrape a daemon for 2 minutes. The
2-minute window is tight: a single scrape miss (15 s default) does
not trip it, but a crashed daemon trips it within one evaluation cycle
after restart.

`up{} == 0` distinguishes scrape failure from the daemon emitting
zero metrics — a daemon that started cleanly but isn't serving requests
will trip this alert, while a daemon that's idle but alive will not.

## Verify

```bash
curl -fsS 'http://127.0.0.1:9090/api/v1/query?query=up{job=~"apid|gatewayd|schedd|vmmd|imaged|builderd|meterd|githubd"}'
systemctl status faas-apid faas-gatewayd faas-schedd faas-vmmd faas-imaged faas-builderd faas-meterd faas-githubd
```

## Check

```bash
journalctl -u <daemon> --since '-5m' --no-pager | tail -50
ss -tlnp | grep <port>   # port per daemon: see deploy/ansible/roles/prometheus/defaults/main.yml
```

A common cause is the daemon process holding its port but its
metrics handler deadlocking — the `up` metric goes to 0 even though
the daemon is technically "running". The status page also surfaces
this as `degraded: ...` (see `pkg/api/dto.go::StatusPage.Degraded`).

## Silence

```bash
amtool silence add \
  --matchers='alertname=FaasDaemonDown' \
  --duration=15m \
  --comment='restarting <daemon>'
```

## Recover

`systemctl restart <daemon>` is the default recovery. If the daemon
keeps crashing, check `/var/log/faas/<daemon>.log` (or the daemon's
journal unit) for the panic stack. The component-ownership rules
(CLAUDE.md) mean a daemon restart is always safe — schedd is the
only writer to `instances`, apid the only writer to apps/deployments,
vmmd the only root component, etc. No other daemon reads from the
restarted daemon's in-memory state.
