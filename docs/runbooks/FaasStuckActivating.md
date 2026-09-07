# FaasStuckActivating

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `node_systemd_unit_state{name=~"faas-.*\.service",state="activating"}`
(from node_exporter `--collector.systemd`, enabled by
`deploy/ansible/roles/node_exporter/templates/node_exporter.service.j2`).
Issue: #573. ADR: ADR-128. Severity: page.

## Symptom

A faas-* systemd unit has been in the `activating` state for 5m
without reaching `active`. The daemon is **not crashlooping**
(distinct from `FaasRestartLoop` — there's no `node_systemd_restart_count`
spike on the same unit). The unit is simply not coming up: it
stuck in the activation graph waiting on a dependency, a config
parse error, or a missing secret.

The 5m threshold is chosen so the alert fires well before the
15m customer-facing SLO breach but well after a normal slow
Postgres connection (~30s) or Vault unseal (~2m).

## Verify

```bash
# Confirm the activating state.
systemctl status 'faas-<name>.service' --no-pager
# Look for: Active: activating (start) / Active: activating (auto-restart)
# / Active: activating (start-pre)

# Where in the activation graph the unit is stuck.
systemd-analyze blame 'faas-<name>.service'
systemd-analyze critical-chain 'faas-<name>.service'
# The critical-chain output names the dependency holding up the
# unit — typically: postgres.service / vault.service /
# etcd-member.service / a network-online.target wait.

# Why the dependency is stuck.
journalctl -u 'faas-<name>.service' --since '5 minutes ago' --no-pager
```

## Check

### Dependency deadlock (most common)

```bash
systemctl list-dependencies 'faas-<name>.service' --no-pager
# For each dep, check if it's in 'active' or 'failed'.

# Postgres:
systemctl status postgresql --no-pager

# Vault:
systemctl status vault --no-pager

# etcd:
systemctl status etcd-member --no-pager
```

The fix is to bring the dependency up first. Once the dep is
`active`, the faas-* unit's activation resumes automatically — no
manual `systemctl start` needed.

### Config parse error

```bash
journalctl -u 'faas-<name>.service' --since '5 minutes ago' --no-pager \
  | grep -E 'config|parse|unknown|invalid|toml|yaml'

# Try the parse in isolation:
/usr/local/bin/faas-<name> -config /etc/faas/<name>.toml -version
# A clean parse prints "<name> <version>" and exits 0; a parse
# error prints the offending line + column and exits non-zero.
```

Recover: fix the offending line in `/etc/faas/<name>.toml` and
`systemctl restart 'faas-<name>.service'`.

### Missing sealed secret

```bash
journalctl -u 'faas-<name>.service' --since '5 minutes ago' --no-pager \
  | grep -iE 'seal|unseal|secret|env.*not set'

# The most common shape is a MISSING env var the daemon reads at
# boot (FAAS_DB_URL, FAAS_VAULT_TOKEN, etc.). Confirm:
grep -rE '^FAAS_[A-Z_]+' /etc/faas/<name>.env
```

Recover: re-seal the missing secret via the runbook
`docs/runbooks/VaultSealRotate.md` (or restore from the off-host
backup for non-Vault env sources), then
`systemctl restart 'faas-<name>.service'`.

### Port collision (rarer than RestartLoop)

```bash
ss -tlnp | grep ':<port>'
# If a stale process from a prior incarnation is holding the port,
# systemd will time out activating after StartLimitIntervalSec
# (default 60s) and the unit enters failed state. The stuck-
# activating signal here is a long activation chain where the
# daemon itself hasn't panicked — different from bind-failure
# in RestartLoop because the daemon never logged an error.

# Recover:
fuser -k '<port>/tcp'   # kill the stale process
systemctl reset-failed 'faas-<name>.service'
systemctl start 'faas-<name>.service'
```

## Follow-up

- Dependency deadlocks are the Tier A reliability story — every
  faas-* unit should have its dependencies on
  `Requires=` + `After=` with explicit health-check timeouts.
  File an ADR if the current dependency graph is missing any
  of these. ADR-066 chain documents the multi-host versions.
- Config parse errors should ideally surface before the unit
  activates — `wire.Daemon()` has a `-version` flag that does a
  config parse dry-run; promote it to a systemd
  `ExecStartPre=/usr/local/bin/faas-<name> -version` so the
  unit fails fast instead of getting stuck activating.
- Missing-secret errors are tracked in `docs/runbooks/VaultSealRotate.md`.
  If the env-source is a non-Vault file, file a follow-up to
  move it under Vault.
