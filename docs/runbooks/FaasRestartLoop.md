# FaasRestartLoop

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `node_systemd_restart_count{name=~"faas-.*\.service"}`
(from node_exporter `--collector.systemd`, enabled by
`deploy/ansible/roles/node_exporter/templates/node_exporter.service.j2`).
Issue: #573. ADR: ADR-128. Severity: page.

## Symptom

A faas-* systemd unit has restarted more than 5 times in the rolling
5-minute window for 2m. The daemon is crashlooping — the systemd
unit is mid-replacement, so customer requests are failing with 5xx
or wake-timeout errors until the unit settles or the operator
intervenes.

Fallback signal (older systemd without `--collector.systemd`, or
when node_exporter's systemd collector is disabled): the daemon's
own `<daemon>_daemon_restart_count{daemon="<name>",version="<ver>"}`
counter, populated by `wire.Daemon()` reading
`$SYSTEMD_RESTARTS_ON_FAILURE`. The alert expression has a
matching fallback in its annotations.

## Verify

```bash
# What node_exporter sees for the systemd unit.
curl -fsS 'http://127.0.0.1:9100/metrics' \
  | grep -E '^node_systemd_restart_count\{name="faas-' \
  | sort

# What the daemon itself reports (the backstop counter).
curl -fsS 'http://127.0.0.1:9104/metrics' \
  | grep -E '^vmmd_daemon_restart_count' \
  | sort
# 9101 apid / 9103 schedd / 9102 imaged / 9106 meterd / 9105 builderd
# 9090 gatewayd-internal / 8080 gatewayd-public.

# Which unit is the offender.
systemctl list-units 'faas-*' --no-pager
```

The first two calls confirm Prometheus's view vs the daemon's view —
the values should agree (the systemd counter is the source of truth,
the daemon counter is the backstop for environments where the
systemd collector isn't running). The third call shows the active
state of every faas-* unit at a glance.

## Check

### Active state per offending unit

```bash
UNIT="<paste from alert labels>"

systemctl status "${UNIT}" --no-pager
# Look for: Active: failed (Result: exit-code) | Active: activating
# (auto-restart) | Active: activating (start-pre) | Active: deactivating

journalctl -u "${UNIT}" -n 200 --no-pager
# Look for: panic / fatal / FAILED / exit status / signal / OOM
# / listen tcp 0.0.0.0:<port>: bind: address already in use
# / Failed to start / dependency failed.
```

Three failure shapes recur in production:

1. **Panic at startup** — a nil-deref or invariant check on the
   boot path. The log will have a goroutine stack trace and a
   "fatal error: runtime error:" line within the last 200 lines.
2. **Bind failure** — `bind: address already in use` from a prior
   instance that hasn't fully released the port yet. Distinct from
   a real crash because the daemon hasn't panicked; it just can't
   listen. Recover: wait for TIME_WAIT to clear (~60s) or kill the
   stale process by PID.
3. **Dependency deadlock** — the unit is waiting on Postgres /
   etcd / Vault that isn't ready yet. This is the same root cause
   as `FaasStuckActivating` — see `docs/runbooks/FaasStuckActivating.md`.

### Mitigate

If the panic / bind failure is reproducible, stop the unit so the
restart loop stops draining capacity on the box:

```bash
systemctl stop "${UNIT}"
systemctl reset-failed "${UNIT}"
# Inspect one boot in detail before starting again.
systemctl start "${UNIT}"
journalctl -u "${UNIT}" -f
```

If the failure is intermittent (transient Postgres hiccup, etc.)
and the unit eventually settles, the alert self-clears once
`increase(node_systemd_restart_count{name=…}[5m])` drops below 5
for 2m. Don't suppress the alert — repeat occurrences escalate to
`FaasRepeatedRestart` (warn severity) and eventually to a tripped
`SecStartLimitBurst` (failed state, customers offline).

## Follow-up

- File a post-incident with the panic signature, the offending
  commit SHA (`grep -m1 git_sha /var/log/faas/<daemon>.log`), and
  the recovery action. Cross-link from `docs/incidents/<date>-<id>.md`.
- If the failure is a real bug (not infra flake), file an issue
  with the panic stack trace and the failing unit's `git_sha`. The
  issue should block the next release cut.
- If the failure is a config / secret / dependency shape, file a
  PR that adds the missing precondition check to `wire.Daemon()`
  so the failure surfaces as a clean error message on the next
  boot rather than a panic at `os.Exit(1)`.
