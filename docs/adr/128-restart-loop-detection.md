# ADR-128 · Restart-loop detection for faas daemons

- **Status:** proposed
- **Date:** 2026-08-24
- **Issue / PR:** [#573](https://github.com/poyrazK/faas/issues/573)
- **Decision:** Ship three Prometheus alerts — `FaasRestartLoop`,
  `FaasRepeatedRestart`, `FaasStuckActivating` — wired off
  node_exporter's `node_systemd_restart_count{name=~"faas-.*\.service"}`
  metric (enabled by `--collector.systemd` in the node_exporter unit)
  with a per-daemon backstop counter `<daemon>_daemon_restart_count{daemon, version}`
  populated by `wire.Daemon()` reading `$SYSTEMD_RESTARTS_ON_FAILURE`.
  Three runbooks (one per alert) cover the triage path and the
  production failure shapes observed in the issue body.

## Context

Issue #573's three unanswered operator questions today:

1. **"Is a daemon crashlooping right now?"** — operators grep
   `journalctl -u faas-<name>.service` after the fact. The signal
   arrives via customer complaints ("deploys failing") rather than
   an operator alert.
2. **"Is a daemon being restarted persistently across the hour?"**
   — operators notice a few `Restart=` lines in journal but don't
   have a threshold for "this is now unstable, intervene before
   systemd gives up at `SecStartLimitBurst`". By the time the unit
   enters the `failed` state, customers are offline.
3. **"Is a daemon stuck in the activating state without ever reaching
   active?"** — the systemd unit shows `activating (start)` for
   minutes at a time when a Postgres / etcd / Vault dependency is
   down. Distinct from crashlooping because there's no
   restart-count spike.

The current observability surfaces are inadequate:

- **`vmmd_ops_total{op}`** and the per-daemon `*_ops_total` cover
  request-side signals but not boot-time lifecycle.
- **node_exporter's `--collector.systemd` is disabled** — the
  ansible role installs node_exporter with `--collector.filesystem`,
  `--collector.netclass`, `--collector.diskstats` but not the
  systemd collector. Operators have no per-unit restart counter on
  Prometheus.
- **Wire.Daemon()** stamps `version` on every slog line but doesn't
  surface a restart-count signal anywhere.

The closed-vocab precedent (ADR-016, ADR-127) for metric labels and
the closed daemon set (apid, gatewayd-public, gatewayd-internal,
schedd, vmmd, imaged, meterd, builderd, gregale — Tier A7 split
kept gatewayd-public and gatewayd-internal as distinct units per
ADR-070) bounds the metric cardinality at 10 series per daemon.

## Decision

### 1. Enable `--collector.systemd` in node_exporter

`deploy/ansible/roles/node_exporter/templates/node_exporter.service.j2`
adds `--collector.systemd` to `ExecStart`. node_exporter exposes
`node_systemd_restart_count{name, type}` and `node_systemd_unit_state{name, state}`
from systemd's D-Bus interface. The Prometheus scrape config
already targets port 9100 (the `node` job in
`deploy/ansible/roles/prometheus/defaults/main.yml`), so the new
metric surfaces immediately.

### 2. Per-daemon backstop counter `<daemon>_daemon_restart_count`

`pkg/wire/metrics.go` adds a new `daemonRestartCount *prometheus.CounterVec`
with closed `{daemon, version}` label set. The constructor
pre-instantiates the cartesian at boot for the 9 closed daemon
names plus an `other` overflow bucket — 10 series per OpsMetrics,
90 fleet-wide. The accessor
`OpsMetrics.RecordDaemonRestart(daemon, version string, n int)`
bumps the counter at `Add(n-1)` so "n restarts" surfaces as
"this process is the nth incarnation" rather than "n increments
across n processes" — operators see a stable per-process counter.

`wire.Daemon()` reads an optional externally supplied
`$SYSTEMD_RESTARTS_ON_FAILURE` value at boot and calls
`defaultOps.RecordDaemonRestart(name, Version, n)` once after
the slog is configured. The env var reads as 0 when unset, which is
the production systemd case, and the counter stays at 0. Alert rules
use node_exporter's systemd metric.

A package-level `defaultOps` field + `RegisterDefaultOps(ops)` setter
is added so each `cmd/<daemon>/main.go` can publish its `*OpsMetrics`
without changing the `RunFunc` signature (one-line plumbing per
daemon).

### 3. Three Prometheus alerts

`deploy/ansible/roles/prometheus/files/faas.rules.yml` adds:

- **FaasRestartLoop** (severity: page) —
  `increase(node_systemd_restart_count{name=~"faas-.*\.service"}[5m]) > 5`
  for 2m. Catches a daemon crashlooping RIGHT NOW — the systemd unit
  is mid-replacement, customer requests are failing.
- **FaasRepeatedRestart** (severity: warn) —
  `increase(node_systemd_restart_count{name=~"faas-.*\.service"}[1h]) > 20`
  for 5m. Catches a daemon racing the systemd `SecStartLimitBurst`
  threshold; the unit will eventually enter the failed state and
  stop being restarted.
- **FaasStuckActivating** (severity: page) —
  `node_systemd_unit_state{name=~"faas-.*\.service", state="activating"} == 1`
  for 5m. Catches a daemon stuck on a dependency deadlock, config
  parse error, or missing sealed secret. Distinct from RestartLoop
  because there's no restart-count spike — the unit is waiting, not
  crashing.

Each alert annotation carries a fallback expression that uses the
per-daemon backstop counter for environments where the systemd
collector is disabled (older systemd, dev runs).

The alertmanager severity-based route table (page → faas-page,
warn → faas-warn) already covers the new alerts — no
`alertmanager.yml.j2` change needed.

### 4. Three runbooks

`docs/runbooks/FaasRestartLoop.md`, `FaasRepeatedRestart.md`,
`FaasStuckActivating.md` mirror the
`FaasApidAuditWriteFailures.md` template (Symptom · Verify ·
Check · Mitigate · Follow-up). Each names the recurring failure
patterns observed in production:

- **RestartLoop:** panic at startup, bind failure, dependency
  deadlock.
- **RepeatedRestart:** transient infra flake (Postgres / etcd /
  Vault hiccup), slow leak (memory / fd / goroutine), sustained
  config drift (TOML key the daemon no longer accepts).
- **StuckActivating:** dependency deadlock, config parse error,
  missing sealed secret, port collision.

## Consequences

- The `restart_count=N` slog attribute on every daemon's boot line
  gives operators a free signal even when the alert pipeline is
  broken (Prometheus down, alertmanager down, etc.).
- The closed-set vocabulary (10 daemon names × 1 version) bounds
  the counter's cardinality — fleet-wide 90 series, well below
  the Prometheus "tens of thousands" guideline.
- systemd has no `RestartCountExport` service directive. Restart-loop alerts
  use node-exporter's `node_systemd_restart_count`; the daemon-local counter is
  non-zero only under a launcher that explicitly supplies the optional env.
- Adding the systemd collector widens node_exporter's metric set
  from ~200 to ~400 series (systemd exposes per-unit / per-service
  state for every unit on the box). The scrape interval is already
  15s so the additional TSDB load is negligible (~50 KB/s).

## Out of scope

- **Off-host log shipping** — Loki / rsyslog pipeline. The systemd
  collector's restart count is enough for the operator surface;
  full log shipping is a separate ADR.
- **Loki vs rsyslog** — out of scope until off-host log shipping
  ships.
- **Per-process resource attribution at restart time** — capturing
  heap / goroutine profile at the moment of restart is a follow-up
  ADR (the `ObserveSidecarRestart` pattern is the precedent but
  only fires for guest-init sidecars, not control-plane daemons).

## References

- ADR-016 (closed-set label vocabulary).
- ADR-070 (Tier A7 gatewayd-public / gatewayd-internal split — the
  closed daemon set is post-split).
- ADR-127 (vmmd-wake-failure observability — sibling ADR; same
  closed-set pattern, same pre-instantiation cartesian).
- ADR-066 (multi-host / compute_nodes — out of scope here; the
  per-daemon counter stays daemon-local until the multi-host rollout).
