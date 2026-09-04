# FaasRepeatedRestart

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `node_systemd_restart_count{name=~"faas-.*\.service"}`
(from node_exporter `--collector.systemd`, enabled by
`deploy/ansible/roles/node_exporter/files/node_exporter.service`).
Issue: #573. ADR: ADR-128. Severity: warn.

## Symptom

A faas-* systemd unit has restarted more than 20 times in the rolling
1-hour window for 5m. The daemon is unstable but hasn't tripped
systemd's `SecStartLimitBurst` (default 5 starts in 10s) yet — once
the limit is hit, the unit enters the `failed` state and systemd
stops restarting it. At that point customers are offline because the
daemon is dead and not being replaced.

This alert is the **early-warning** for the FaasRestartLoop signal —
the daemon is racing the limit without crossing the catastrophic
threshold of 5 restarts in 5m. Operationally important because
operators tend to ignore "daemon restarted a few times" until it
becomes "daemon is dead".

Fallback signal: the daemon's own
`<daemon>_daemon_restart_count{daemon="<name>",version="<ver>"}`
counter, populated by `wire.Daemon()` reading
`$SYSTEMD_RESTARTS_ON_FAILURE`. The alert expression has a matching
fallback in its annotations.

## Verify

```bash
# Per-unit restart count over the last hour.
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=increase%28node_systemd_restart_count%7Bname%3D~%22faas-.%2A%5C.service%22%7D%5B1h%5D%29'

# Cumulative restart count (the systemd side).
systemctl show 'faas-*' --property=Names,NRestarts,ActiveState,SubState \
  --no-pager

# Recurring failure signature.
journalctl -u 'faas-<name>.service' --since '1 hour ago' --no-pager
```

The first query shows the rate that Prometheus is using; the second
shows the cumulative count which doesn't grow past 5 because of
`SecStartLimitBurst` once the unit enters the failed state.

## Check

### Recurring failure pattern

```bash
UNIT="<paste from alert labels>"

journalctl -u "${UNIT}" --since '1 hour ago' --no-pager \
  | grep -E 'panic|fatal|FAILED|Failed to start|exit status|signal'
```

The most common patterns:

1. **Transient infra flake** — Postgres connection reset, etcd
   leader election, Vault seal. Each flake triggers a panic and
   systemd restarts. The unit eventually stays up once the
   dependency settles. The alert self-clears once the rate drops
   below 20/h for 5m.

2. **Slow leak** — memory leak, fd leak, goroutine leak. The
   daemon OOMs every 20-40 minutes. The rate is well below the
   RestartLoop threshold (5/5m) but accumulates. The signature is
   `oom-kill` in `dmesg` and `Memory cgroup out of memory` in
   `journalctl`.

3. **Sustained config drift** — a TOML key that the daemon no
   longer accepts (`unknown field` from a strict decoder). The
   daemon panics on the first read. The signature is the same
   panic line repeated every restart.

### Mitigate

For (1), wait for the alert to self-clear once the dependency
recovers. File a follow-up issue if the dependency is on a
single-node Postgres (Tier A redundancy requirement, ADR-066
chain).

For (2), `systemctl stop "${UNIT}"` to break the loop, then
capture a heap profile (see the heap-profiling runbook) before
restarting. File an issue with the profile — the leak is a real
bug that will recur.

For (3), stop the unit, fix the config, restart. File a PR that
either (a) makes the decoder lenient on unknown fields (backward
compat) or (b) adds a startup check that validates the config
shape before the daemon calls any external service.

## Follow-up

- `FaasRepeatedRestart` and `FaasRestartLoop` share the same
  root-cause taxonomy — file the post-incident under whichever
  fires first and cross-reference the other.
- Track the recurring failure patterns in `docs/incidents/` —
  the same root cause appearing 3+ times across a quarter is
  the trigger for a "real fix" ADR rather than another
  triage-and-patch.
