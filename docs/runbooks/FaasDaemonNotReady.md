# FaasDaemonNotReady

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `<daemon>_daemon_ready{daemon="<name>"}` mirrored from the
daemon's aggregate `/readyz` probe. Severity: page.

## Symptom

Prometheus can scrape the daemon, but its readiness state has stayed at
`0` for 5 minutes. This covers dependency failures, incomplete warm-up,
and an intentional drain; it is distinct from `FaasDaemonDown`.

## Verify

```bash
# Prometheus runs on :9095 in the deployed control plane.
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=<daemon>_daemon_ready{daemon="<name>"}'

# Ask the daemon for the dependency-specific reason.
curl -i 'http://127.0.0.1:<metrics-port>/readyz'
systemctl status faas-<name> --no-pager
journalctl -u faas-<name> --since '-10m' --no-pager | tail -100
```

The `/readyz` body is the source of truth for the reason. Check the
corresponding dependency first (Postgres, cache, subscription, storage,
or peer dial) before restarting a healthy process.

## Silence

```bash
amtool silence add \
  --matchers='alertname=FaasDaemonNotReady,daemon=<name>' \
  --duration=30m \
  --comment='planned warm-up or drain for <name>'
```

## Recover

Resolve the dependency named by `/readyz`, then confirm the metric returns
to `1`. For an unplanned stuck state, restart only the affected unit:

```bash
sudo systemctl restart faas-<name>
```

If the alert repeats, preserve the `/readyz` response and the last 10
minutes of journal output for the owning daemon; the readiness observer
deliberately keeps the metric and endpoint aligned for this comparison.
