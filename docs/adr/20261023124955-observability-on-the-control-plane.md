# ADR-20261023124955: Observability backend on the control plane

- **Status:** accepted (owner, 2026-10-23: "Start working on this matter then"). The §7 thresholds and the §1 sizes stay provisional until the gate runs.
- **Date:** 2026-10-23
- **Amends:**
  - [ADR-143](143-off-host-log-pipeline.md): Loki moves from a dedicated `observability` host to the control plane. This reverses ADR-143's rejection of "Run Loki on the control plane". Chunks go to object storage, which answers the reason that ADR gave.
  - Spec §12 ("Logs: journald → Loki free tier") and spec §13 (adds a `faas-obs.slice` row). The spec text changes in the implementing PRs, after acceptance.
- **Related:**
  - [ADR-031](031-self-hosted-grafana.md) (Grafana access).
  - [ADR-097](097-schedd-wake-phase-telemetry.md) (`wake_id` exemplars and wake-phase events).
  - [ADR-127](127-production-debugger.md) (the production debugger).
  - [ADR-1155](1155-on-demand-cpu-and-heap-profiling.md) (profiling).
  - [ADR-1236](1236-paging-set.md) (the paging set).
  - [ADR-083](083-active-passive-ha-topology.md) (the standby control plane).
- **Decision in one line:** run the whole observability backend on the control plane, inside one capped, low-priority slice. Logs go to Loki, with chunks in object storage. Metrics stay in Prometheus, kept for 35 days. There is no trace backend in this phase. An external probe that does not depend on the control plane covers the case where the control plane is down.

## Context

Gregale emits a lot of signal:

- 399 alert rules: 33 page, 359 warn, 7 info. Every page alert has a firing fixture.
- 19 Grafana dashboards, with CI gates that every metric a rule or panel reads is emitted.
- The `gregalectl obs` suite: health, incidents, triage, evidence, bundle, overview and capacity.
- Correlation fields on every daemon log line: `request_id`, `wake_id`, `trace_id` and `span_id`, from `pkg/wire/logging.go`.
- `wake_id` exemplars on the wake histograms.
- The wake-phase event timeline.
- On-demand profiling.

What operators cannot do today:

- **Search logs across hosts.** Daemon logs stay in each host's journald, capped at 512 MiB / 7 days. The Loki and Promtail roles exist, but only `make bootstrap-observability` (`deploy/ansible/site.yml`) installs them, against an `[observability]` host that no environment has. CD never runs that playbook. Diagnosing one slow wake means SSH and `journalctl` on each node.
- **Keep metrics past 15 days.** One Prometheus 2.54.1 on the control plane keeps 15 days (`prom_retention_days`) and has no `remote_write`. The public monthly SLOs (99.5 % API availability, wake p95 < 1 s, build success 99 %; spec §12) cannot be computed.
- **See traces.** Spans exist in `pkg/gateway`, `pkg/sched`, `pkg/fcvm` and `pkg/wire/otelgrpc.go`. Export needs a hand-written `/etc/faas/otel.env` on every host, and there is no collector or backend.
- **Notice a serving outage from outside.** `synthetic-canary.yml` exercises the deploy path every 6 h. It pages by pushing to the control plane's own Alertmanager over SSH, so it goes quiet when the control plane is down.
- **Keep observability away from Postgres.** Prometheus, Grafana and Alertmanager run in `system.slice` with no `MemoryMax`. That slice's 2,048 MB budget is for the OS (spec §13). PostgreSQL runs beside them in `system-postgresql.slice`, weighted `CPUWeight=1000` (roles/postgres, 2026-10-21). Prometheus has `CPUWeight=50`; Grafana and Alertmanager have the default 100. Under contention the three therefore take about a fifth of the CPU PostgreSQL's slice gets, and nothing bounds their memory.

Two constraints shape this decision:

1. The owner decided on 2026-10-23 that there is no dedicated machine for observability for now: everything runs on the control plane.
2. The control plane has little spare capacity. In production-us it is 2 vCPU / 16 GB. Postgres uses about 60–80 % of a core under idle customer load (measured 2026-10-20). Prometheus uses about 11 %. Two self-hosted GitHub runners burst to about 90 % of a core per running job. The root disk has 107 GB free (45 % used, 2026-10-23) but cannot grow, because the project's SSD quota is used up.

Measured on the production-us control plane on 2026-10-23 (read-only):

  | Unit | RSS | Peak since start |
  |---|---|---|
  | Prometheus (104k head series, 12 GB on disk for 15 days) | 1.24 GB | 1.74 GB |
  | Grafana | 106 MB | 181 MB |
  | Alertmanager | 33 MB | 46 MB |
  | PostgreSQL | 5.0 GB | 6.8 GB |

  journald holds 257 MB. Loki and Promtail are not installed and `/etc/faas/otel.env` is absent.

## Decision

### 1. One capped, low-priority slice for observability

- A new `faas-obs.slice` on control-plane hosts, written by the `systemd_slices` role:

  | Setting | Value |
  |---|---|
  | `MemoryMax` | 3,584 MB |
  | `CPUWeight` | 20 (`faas-cp.slice` stays at the default 100) |
  | `CPUQuota` | 100 % (one core at most) |
  | `IOWeight` | 20 |

  Against `system.slice` (100, which holds PostgreSQL) it gets about 17 % under contention, close to the share the three units have today, and the same against `faas-cp.slice`. Inside the slice, Prometheus and Alertmanager (100) outrank Grafana (25). A runaway query or compaction can never take more than one core.
- Its members are `prometheus`, `alertmanager`, `grafana-server`, `loki`, and the control plane's `promtail`. Each unit gets `Slice=faas-obs.slice`.
- Each Go process gets `GOMEMLIMIT` at about 85 % of its own limit:

  | Unit | Memory limit | `GOMEMLIMIT` |
  |---|---|---|
  | Prometheus | `MemoryHigh` 2,304 MB | 2,048 MiB |
  | Loki | `MemoryMax` 768 MB | 640 MiB |
  | Grafana | `MemoryHigh` 256 MB, `MemoryMax` 384 MB | — |
  | Alertmanager | `MemoryMax` 128 MB | — |
  | Promtail | `MemoryMax` 256 MB (the spec §13 agent budget) | — |

  Prometheus gets `MemoryHigh` instead of `MemoryMax`, so it is throttled rather than OOM-killed and restarted with a cold head. The slice's `MemoryMax` remains the hard fence.
- Query limits stay at Prometheus's defaults. Rule evaluation shares the query engine with dashboards, and a lower concurrency or sample limit could fail rule groups silently. If the gate sees memory spikes from long dashboard ranges, revisit them.
- Spec §13 gains the row `faas-obs.slice · 3,584 MB · prometheus, alertmanager, grafana, loki, promtail (control plane only)`. Compute hosts keep their existing Promtail unit (`MemoryMax=256M`), which is already inside the "loki/promtail agents 256" line.
- These sizes are proposals. The gate in §7 confirms or changes them before production.

### 2. Logs: Loki on the control plane, chunks in object storage

- **Loki 3.5.3** (the pinned `loki` role), single binary, on the control plane.
  - It listens on loopback for Grafana and the local Promtail.
  - It also listens on the control plane's private (overlay) address with mTLS, for compute-node Promtail.
  - nftables admits port 3100 only from the fleet's private CIDR. Nothing binds a public interface.
- **Storage:**
  - The TSDB index and chunks go to an S3-compatible bucket, either a dedicated bucket or a `loki/` prefix with a key scoped to it. The S3 API is used everywhere, including GCS through HMAC interop, so the design does not depend on one provider. The gate must exercise the compactor's deletes against the production bucket's provider.
  - Credentials reach Loki through a root-only `0400` `EnvironmentFile`, using the same pattern as `/etc/faas/otel.env`, and `-config.expand-env`. They never appear in the rendered config or the inventory.
  - Local disk holds only the WAL, the index cache and the compactor's working directory, which stay small at this ingest rate; the host disk alerts watch them. The control plane's disk cannot grow and is not where logs live.
- **Retention:** 14 days (`loki_retention_period: 336h`), enforced by the compactor. A bucket lifecycle rule at 16 days is the backstop.
- **Why this answers ADR-143's objection:**
  - Stored logs live in the bucket, so losing the control plane loses only its un-flushed head: minutes of the control plane's own logs.
  - Compute nodes keep their journals and Promtail positions. They replay up to `promtail_journal_max_age` (12 h) into a recovered or replacement control plane.
- **Ingest limits:** keep the role's limits (4 MB/s rate, 6 MB burst, 2,000 streams, 500 query series, 5,000 entries per query).
- **Shipper:** the existing Promtail role, with three changes.
  1. The unit allowlist (`promtail_journal_units`) is stale. It lists 13 units and misses, among others, `faas-realtimed`, `faas-outboundd`, `faas-bridged`, `faas-profiled`, `faas-livequeryd`, `faas-maild`, `faas-s3-gatewayd`, `faas-gatewayd-public-edge`, the apid workers, `postgresql@*` and `loki` itself. It is brought up to date. A unit test fails when a `faas-*.service` under `deploy/` has no allowlist entry, in the same spirit as `TestAlertMetricsAreEmitted`.
  2. A `drop` stage keeps `level=DEBUG` lines in the local journal, and an `output` stage stores the daemon's own line instead of journald's JSON envelope around it (host and unit are labels already). One journal reader serves every unit, because sd-journal ORs same-field matches; the old one-reader-per-unit layout would have run 41 readers on the 2-vCPU control plane.
  3. `level` becomes a label, because it is a bounded set. The ADR-143 label rule is otherwise unchanged. `request_id`, `wake_id`, `trace_id` and `span_id` stay in the line content, never in labels.
- **Promtail end of life:** Grafana deprecated Promtail in favour of Alloy, and its support ended in March 2026. Phase 1 uses the pinned, CI-checked Promtail role because it already exists and is the smallest change. [ADR-20261023185953](20261023185953-replace-promtail-with-alloy.md) chooses Grafana Alloy, with Vector as the fallback if Alloy misses its budget in the gate. It is due before logs are enabled in a second environment.
- **mTLS from the fleet CA:** the log pipeline's leaves are `pkg/pki` feature leaves (`platform-logs`), which no host role requires, so they never block a rollout (the rc.255 failure mode). The `platform_logs_pki` role issues them: Loki's server leaf (`loki.faas`) plus the Promtail, Prometheus and Grafana client leaves on the control plane, and a Promtail leaf per compute node, staged on the control plane and copied in the way realtimed's live-query leaf is. `pki_renew.yml` renews them daily. Loki verifies clients against the fleet CA, so the private-network rule on port 3100 is what limits who can connect.
- **Deployment:**
  - `loki` (control plane) and `promtail` (control plane and compute) move from `site.yml` into the control-plane and compute plays of `bootstrap.yml`. CD therefore converges them through `control_plane_converge.yml` and `cd-compute.yml`, and its role-plan selection includes both.
  - Both are gated by the operator variable `faas_obs_logs_enabled` (default false), the same way optional control-plane services such as `faas_livequeryd_managed` are. `faas_obs_loki_address` (the control plane's private address) and `faas_obs_loki_server_name` derive the Promtail push URL, Loki's bind address, Prometheus's Loki and Promtail scrape jobs, Grafana's datasource and the nftables openings (3100 compute→control plane, 9080 control plane→compute).
  - The same flag drives `prom_promtail_enabled` and the Loki meta-monitoring target.
  - The `[observability]` inventory group, `site.yml`'s Loki play and `make bootstrap-observability` are retired.
- **Access:**
  - The Grafana Loki datasource, with derived fields that link `trace_id` / `wake_id` to the events timeline that `gregalectl obs evidence` and the debugger already show.
  - `gregalectl obs logs` on the control plane. It searches by wake, request or trace id, host, unit, level or text, or takes raw LogQL, and reads Loki directly over mTLS with the settings the loki role renders to `/etc/faas/loki-client.env`. `logcli` is not installed.

### 3. Metrics: Prometheus stays on the control plane and keeps 35 days

- **Retention:** `--storage.tsdb.retention.time=35d` and `--storage.tsdb.retention.size=<per host>`, whichever comes first. The size cap defaults to 40 GB and is set per host. 35 days covers a calendar month plus slack for the monthly SLO.
- **Estimate:**
  - On 2026-10-23 production-us held 12 GB for 15 days at 104k head series, about 0.8 GB/day, or about 28 GB at 35 days. The root disk has 107 GB free.
  - Part of that 15-day window predates the #4675 series cut, so steady state is probably lower. `prometheus_tsdb_storage_blocks_bytes` shows the real figure after rollout.
- **If the disk cannot hold 35 days:**
  - First, drop the bucket series of the per-deployment request-duration histogram at scrape and keep its `_count` / `_sum`. It was the largest family on 2026-10-23, at 10.8k of 104k head series.
  - Only after that, shorten retention, and never below 31 days.
- **SLO recordings:**
  - A new `faas_slo_monthly` rule group records, without per-app labels, 5-minute good/total rates for the three public SLOs:
    - API availability, from `gateway_platform_errors_total` and request totals;
    - wake p95, from `gateway_wake_latency_seconds` buckets summed over apps;
    - build success, from `builderd_ops_total` excluding `user_error`.
  - Each rule gets a promtool fixture.
  - A fleet-dashboard panel shows the rolling 30-day ratios.
- **No `remote_write`, Thanos or second Prometheus:** with no second machine there is nowhere to write to. Thanos's sidecar, store and compactor would cost more CPU and RAM on the control plane than a longer local retention.

### 4. Traces: no trace backend in this phase

- OTLP export stays off: no `/etc/faas/otel.env`, no collector, no Tempo. A collector plus a trace store is a second process family that the control plane cannot be shown to afford until §7's numbers exist.
- Operators get per-request correlation from what is already emitted:
  - The correlation fields on every daemon log line become searchable across hosts in Loki.
  - `wake_id` exemplars lead from a slow histogram bucket to a specific wake.
  - The events table's wake-phase timeline (`trace_id` column) gives the per-wake phase breakdown.
  - ADR-1155 profiling covers CPU and heap.
- **Revisit when both hold:**
  - §7 shows the slice below half its CPU and memory limits for a week;
  - an incident needed cross-daemon span timing that logs and wake-phase events could not give.

  The next step would be a sampled Tempo on the control plane with an object-storage backend: 1 % of traces, plus all errors and wakes over 1 s.

### 5. Alarms that do not depend on the control plane

- **Dead-man's switch (unchanged):** `FaasWatchdog` posts to an external heartbeat service (`am_heartbeat_url_file`). A control plane already refuses to bootstrap without it.
- **New serving-path probe** (`serving_probe` role, `docs/ops/serving-probe.md`):
  - A systemd timer on every compute node requests one or more platform-owned probe-app URLs through the public edge every 5 minutes, and records the status and time to first byte in the node_exporter textfile directory.
  - It runs on the compute nodes because GitHub-hosted runners cannot reach the public host: Cloudflare blocks their egress (the reason `synthetic-canary.yml` and `cd-platform.yml` originate their checks from the control plane). The self-hosted runners live on the control plane, so they share its fate.
  - The probe app's idle timeout is shorter than the probe interval, so every probe exercises a wake and the app parks between probes. It must never be kept warm: an app held warm blocks node drains (the rc.290 fsn-3 outage).
  - A URL fails when two attempts 30 s apart both miss a 2xx. The node posts to the chat webhook directly. The webhook is a root-only credential on each node, and the role refuses to install the probe without it. Notices go out on failure and recovery, with at most one reminder an hour.
  - A fully passing run pings an optional per-node external heartbeat check, so a probe that stops running is noticed too.
  - Each compute node reports on its own. A fleet-wide outage arrives once per node, and losing one node does not silence the probe.
  - It is off until an operator lists probe URLs.
- **`synthetic-canary.yml`:** when its Alertmanager push fails, it posts to the `CANARY_FALLBACK_WEBHOOK_URL` secret instead. GitHub can reach the chat service; only the public Gregale host is blocked.

### 6. Access

Grafana stays on loopback (`127.0.0.1:3000`), reached through the existing SSH/IAP tunnel (ADR-031). This ADR exposes nothing new.

### 7. Measurement gate before production

- **Where:** a non-production control plane built from the same manifest shape (a scratch control plane or the native acceptance pair). It must never be the production control plane, which is saturated.
- **What:** realistic log volume, replayed from a production journald export of one day, plus the normal scrape load.
- **How long:** 72 h, covering both idle and running-GitHub-job periods.
- **How:** `scripts/ops/obs_gate.py --window 72h --baseline-end <end of a 72 h window before the stack was enabled>` runs every check below against Prometheus and prints PASS, FAIL or SKIP per check, then GO or NO-GO. The per-slice numbers come from `faas-cgroup-metrics` (role `cgroup_metrics`), which also feeds the "Control plane resources" dashboard.
- **Go/no-go thresholds:**

  | Check | Threshold |
  |---|---|
  | `faas-obs.slice` CPU | ≤ 0.25 cores averaged over the window; ≤ 0.60 cores over any 5 min |
  | Slice memory | peak anonymous memory ≤ 80 % of `MemoryMax`; zero OOM kills |
  | PostgreSQL | CPU stall share (cgroup `cpu.pressure` some) at most 5 points above the baseline window |
  | Wakes | platform wake p95 at most 10 % above the baseline window |
  | Prometheus disk | 35-day projection ≤ `prom_retention_size` |
  | Promtail | zero dropped entries |

  PostgreSQL's CPU stall share replaces the earlier `select 1` p99 check: it measures the same contention, comes from the same exporter as the other checks, and needs no probe against the database.
- **Recorded in this ADR on acceptance:** the script's output, plus what it cannot measure:
  - Loki ingest bytes/s;
  - object-storage requests per day and GB stored, priced as a monthly cost.
- **On failure:** keep §1, §3 and §5. Ship logs only from the control plane and the three highest-value daemons (`schedd`, `vmmd`, `gatewayd-internal`), or shorten retention, then re-measure.
- **Production rollout:** one environment first. Setting `faas_obs_logs_enabled: false` is the rollback: the next converge stops and disables Loki and Promtail (`role_convergence`).

#### Smoke run, 2026-10-24 (not the gate)

The gate is still owed. No host for it could be created that day: the
`gregale-prod` project was at 12 of 12 CPUs and 250 of 250 GB SSD, and fsn-4's
project had billing disabled.

**Where and how.**
- The roles were converged once on the shared `gregale-internal-test-1` (2 vCPU),
  then removed:
  - `node_exporter`, `prometheus`, `loki`, `alloy` and `cgroup_metrics`;
  - `faas-obs.slice`;
  - `faas_box_role: control-plane`.
- The configuration was the production one: mTLS on fleet-PKI leaves from a
  throwaway CA (`gregalectl pki issue-feature`), and Loki bound to the private
  address. Storage was the exception: Loki used the filesystem, through a
  one-line patch in a scratch copy of the role.
- Production's log rate was measured read-only first: about 1,930 lines
  (495 KB) per hour across the control plane and both compute nodes.
- A transient unit replayed slog-shaped lines at 10×, 100× and 1000× that rate,
  20 minutes each.

**What the first real converge found** (all fixed on this branch):
- The Loki release pin had never matched `loki-linux-amd64.zip`.
- Loki exited at startup: the unit's address-family sandbox blocks the
  interface lookup it uses for its ring address.
- `faas-cgroup-metrics` wrote nothing for `faas-cp.slice`, `faas-obs.slice` or
  `faas-tenant.slice`: systemd nests them under `faas.slice`.
- PostgreSQL's server log is a file (`/var/log/postgresql/`), not the journal.
  The allowlisted `postgresql@16-main.service` ships nothing. Shipping the file
  is open: its statement text and `DETAIL` key values can hold customer data.

**End to end.** Journal → Alloy → Loki (mTLS) → `gregalectl obs logs` (mTLS)
worked. Prometheus scraped Loki over mTLS and Alloy on 9080. All 524 rules
loaded and evaluated without errors.

**Per phase** (steady 15 minutes of each):

| Phase | Lines/s into Loki | Slice CPU, average / max | Slice anon peak | Loki anon | Alloy CPU / anon | Dropped |
|---|---|---|---|---|---|---|
| 10× | 6.1 (1.8 KiB/s) | 0.023 / 0.030 cores | 231 MB | 86 MB | 0.005 cores / 54 MB | 0 |
| 100× | 54.8 (16.2 KiB/s) | 0.036 / 0.043 cores | 291 MB | 134 MB | 0.014 cores / 63 MB | 0 |
| 1000× | 371.5 (110 KiB/s) | 0.094 / 0.120 cores | 392 MB | 233 MB | 0.067 cores / 65 MB | 0 |

**Notes on the numbers.**
- At 1000× journald's per-unit rate limit (10,000 lines per 30 s) suppressed
  the excess before Alloy saw it. One daemon cannot push more than about
  330 lines/s into the pipeline.
- `obs_gate.py --window 60m --shipper alloy` passed every check it could
  measure.
- It skipped the PostgreSQL and wake checks, which have no baseline without a
  control-plane workload.
- Its Prometheus disk projection read 0, because no TSDB block is compacted
  within an hour.
- This Prometheus scraped a few thousand series, not production's 104k, so its
  numbers say nothing about production's Prometheus.

### 8. Rollout as small PRs

1. `faas-obs.slice`; `Slice=`, memory limits and `GOMEMLIMIT` on Prometheus, Alertmanager and Grafana; spec §13 row. Only the caps change behaviour.
2. Prometheus 35-day and size-capped retention; the `faas_slo_monthly` group with fixtures; the dashboard panel; the spec §12 SLO note.
3. The `platform-logs` feature leaves in `pkg/pki`, the `platform_logs_pki` role and its renewal play.
4. The Loki role: object-storage backend, credential `EnvironmentFile`, control-plane placement, `faas-obs.slice`, 14-day retention.
5. Promtail: allowlist refresh, the unit-coverage test, one journal reader, the debug `drop` stage, the `level` label, `faas-obs.slice`.
6. `faas_obs_logs_enabled` and the derived variables; `bootstrap.yml` wiring (CD converges it); the nftables openings; retirement of `site.yml`'s Loki path, the `[observability]` group and `make bootstrap-observability`; spec §12 log line.
7. The `serving_probe` role on compute nodes and `docs/ops/serving-probe.md`; the direct-webhook fallback in `synthetic-canary.yml`.
8. `docs/ops/logs.md` with query recipes (by `wake_id`, `trace_id`, host, unit), and an update to the `FaasLokiPipelineDegraded` runbook.

PRs 1, 2 and 7 do not depend on logs and can ship before the gate.

### Operator steps

- Create the Loki bucket or prefix and a key scoped to it. Install the credential file (`root:root 0400`) on the control plane.
- After the gate, set `faas_obs_logs_enabled: true` with `faas_obs_loki_address` and the bucket variables per environment (`docs/ops/logs.md`). Certificates need no operator step.
- Create the probe app. Write the chat webhook (and optional heartbeat) credentials on each compute node, then list the probe URLs (`docs/ops/serving-probe.md`). Set the `CANARY_FALLBACK_WEBHOOK_URL` repository secret.
- Confirm the heartbeat and warn-webhook files exist on each control plane. This is still owed from the ADR-1236 rollout.

## Consequences

**Gains**

- Cross-host log search for 14 days. It survives a compute-node loss, and a control-plane loss apart from the control plane's un-flushed minutes.
- 35 days of metrics, and computable monthly SLOs.
- A serving outage is detected by something other than the host that serves the control plane.
- Observability can no longer take memory from the OS budget. Under CPU pressure Postgres wins: scrapes may lag and Promtail may back off, and both show up on existing alerts (`FaasLokiShippingStalled`, `FaasLokiShippingDropped`, `FaasPrometheusRuleEvaluationFailed`; runbooks `FaasLokiPipelineDegraded` and `FaasPrometheusAlertingPathDegraded`).

**Costs**

- A control-plane outage still takes Prometheus, Alertmanager, Grafana and Loki down with it. Detection then rests on the heartbeat and the external probe. Diagnosis falls back to each node's journald (7 days) until the control plane is back. After that Loki has the bucketed history plus up to 12 h of replay.
- New recurring cost: object storage for 14 days of logs, priced from the gate.
- One more stateful service on the busiest host.

**Revisit when** a standby control plane (ADR-083) is running. Moving `faas-obs.slice` to the standby would remove the shared-fate cost without buying a machine.

## Rejected alternatives

- **Dedicated observability host (ADR-143 as written):** the owner decided on 2026-10-23 that there is no budget for it now.
- **Loki with filesystem storage on the control plane:** the disk cannot grow, and losing the control plane's disk would lose every log. That was ADR-143's objection.
- **`systemd-journal-remote` into the control plane:** no index or query beyond `journalctl`, it stores on the control plane's disk, and it has no Grafana correlation.
- **A hosted log and trace vendor (spec §12's "Loki free tier", Datadog):**
  - It adds a subprocessor for the customer app-log records that `gatewayd-internal` writes to journald, which means a DPA and subprocessor-list change.
  - Free tiers cap volume and retention, and charge overage beyond them.
  - It is worth reopening if the gate fails.
- **An OpenTelemetry Collector and Tempo now:** the CPU and memory cost on the control plane is unmeasured. Deferred under §4.
- **Prometheus `remote_write` or Thanos to object storage:** more processes and RAM on the control plane than a longer local retention, and nowhere else to query from.
- **Running the probe on the self-hosted runners:** they run on the control plane, so they fail together with what they watch.
- **A GitHub-hosted scheduled probe:** Cloudflare blocks GitHub-hosted egress to the public host. A Cloudflare exception keyed on a secret header would depend on which bot protection the zone runs, and some modes cannot be skipped.
