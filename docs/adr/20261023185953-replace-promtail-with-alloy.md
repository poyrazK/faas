# ADR-20261023185953: Replace Promtail with Grafana Alloy as the journal shipper

- **Status:** accepted (owner, 2026-10-23: "implement the Alloy ADR next"). Rollout PRs 1–3 are implemented together; PR 4, the removal, waits a release.
- **Date:** 2026-10-23
- **Amends:**
  - [ADR-20261023124955](20261023124955-observability-on-the-control-plane.md) §2, whose follow-up this is: "choosing the replacement shipper … is due before logs are enabled in a second environment".
  - [ADR-143](143-off-host-log-pipeline.md): "Run a pinned Promtail instance on every host".
- **Decision in one line:** ship journald to Loki with Grafana Alloy instead of Promtail, behind a `faas_obs_log_shipper` switch. Alloy keeps the same pipeline, labels, mTLS leaf, metrics port and Prometheus job names. Vector is the named fallback if Alloy does not fit its budget in the measurement gate.

## Context

- **Promtail is end of life.** Grafana deprecated it on 2025-02-13 (long-term support: critical fixes only), and it reached end of life on 2026-03-02. No further updates are published, and feature work moved to Alloy. Gregale pins Promtail 3.5.3 in `deploy/ansible/roles/promtail`, so any CVE in it from now on stays unfixed. ADR-20261023124955 put Promtail on every host and on the control plane, which makes it a long-lived, root-adjacent process: it reads the whole journal through the `systemd-journal` group.
- **The shipper's contract today** (ADR-143, ADR-20261023124955 §2), which any replacement must keep:
  - **R1. Read.** Read journald for an explicit unit allowlist: every `faas-*` unit, PostgreSQL and the observability stack, pinned by `TestPromtailShipsEveryFaasUnit`. Use one reader, and resume from a persisted cursor after a restart, replaying at most `promtail_journal_max_age` (12 h).
  - **R2. Shape.**
    - Unwrap journald's `MESSAGE`, then parse the daemon's slog JSON.
    - Promote only `account_id`, `app_id`, `daemon` and `level` to labels; arbitrary customer fields never become labels (ADR-143).
    - Drop `level=DEBUG`.
    - Store the daemon's own line, not journald's envelope.
    - Add the fixed labels `job="faas-journal"`, `host`, `environment` and `unit`.
  - **R3. Deliver.**
    - Push to Loki at `https://<faas_obs_loki_address>:3100` with tenant `gregale`.
    - Authenticate with the fleet-PKI client leaf `/etc/faas/tls/promtail/loki-client.{crt,key}`, trust the fleet CA, and verify the server as `loki.faas` (a server-name override, because the URL is an IP address).
    - Bounded retry, then drop, and count what was dropped.
  - **R4. Never block a request.** Delivery is asynchronous; a Loki outage costs logs, never latency.
  - **R5. Be observable.**
    - Serve Prometheus metrics on port 9080 of the host's private address (control plane: loopback). apid's HTTP service discovery hard-codes `host:9080` (`pkg/apid/apidserver/metrics_discovery.go`), and `cd-controlplane.yml` requires the `promtail` jobs to be up after a rollout.
    - Expose sent, dropped and read counters for the `faas_loki_pipeline` alerts and the Loki pipeline dashboard.
  - **R6. Fit the budget.** At most 256 MB (spec §13 "loki/promtail agents") inside `faas-obs.slice`, with CPU that does not show up in PostgreSQL's CPU stall share on the 2-vCPU control plane (ADR-20261023124955 §7, `scripts/ops/obs_gate.py`).
  - **R7. Supply chain.** A pinned release artifact with a published checksum that the role verifies, like the Loki and Promtail roles do today, and a license compatible with the repository.
  - **R8. Hardening.** Run unprivileged, with only journal read access, behind the systemd hardening gate (`scripts/ci/check_systemd_hardening.sh`). No outbound traffic other than Loki; in particular, no usage reporting.

## Options

| | Grafana Alloy | Vector | Fluent Bit | OpenTelemetry Collector (contrib) | Keep Promtail |
|---|---|---|---|---|---|
| Upstream status | Promtail's successor, active | active | active (CNCF) | active | **end of life** |
| R1 journald | `loki.source.journal`: matches, relabeling, positions | `journald` source (runs `journalctl`) | `systemd` input with a cursor DB | `journald` receiver (runs `journalctl`) | yes |
| R2 shape | `loki.process` has the same stages as Promtail (`json`, `drop`, `labels`, `output`) | VRL `remap` | `parser` + `grep` + `record_modifier` + `drop_single_key` | `transform` processor | yes |
| R3 mTLS with a server-name override | `tls_config { server_name }`, the same client config Promtail uses | override not found in the sink's TLS options; needs a resolvable `loki.faas` | `tls.vhost` is SNI; verification semantics unclear | yes | yes |
| R5 metrics on :9080 | built-in HTTP server | `prometheus_exporter` sink | built-in HTTP server | yes | yes |
| R6 footprint | the largest of these. Grafana's sizing guide: about 1 core and 120 MiB per 1 MiB/s of logs, plus a fixed per-node overhead | small (Rust) | smallest (C) | large binary, moderate RSS | known |
| R7 artifact | GitHub release zip with checksums | static musl tarball with checksums | apt repository or a source build | GitHub release | pinned |
| Migration | `alloy convert --source-format=promtail` | rewrite | rewrite | rewrite; Loki labels come via OTLP resource mapping | none |

## Decision

1. **Grafana Alloy replaces Promtail on every host** that ships logs, in a new `alloy` role. Promtail stays available behind `faas_obs_log_shipper: promtail` for one release so a rollback is a variable change, then the `promtail` role is deleted.
2. **The pipeline is translated, not redesigned.** Start from `alloy convert --source-format=promtail` on the rendered Promtail config, then keep a hand-maintained template:
   - `loki.source.journal`:
     - one reader whose `matches` OR the unit allowlist (the list moves from `promtail_journal_units` to the `alloy` role and stays pinned by the unit-coverage test);
     - `relabel_rules` that set `unit` from `__journal__systemd_unit`;
     - fixed labels `job`, `host` and `environment`;
     - `max_age` 12 h.
   - `loki.process`, with Promtail's stages one for one:
     1. `stage.json` (MESSAGE → message);
     2. `stage.json` (message → `account_id`, `app_id`, `daemon`, `level`);
     3. `stage.drop` (`level == DEBUG`);
     4. `stage.labels`;
     5. `stage.output` (message).
   - `loki.write`:
     - the endpoint derived from `faas_obs_loki_address`, tenant `gregale`;
     - `tls_config` with the fleet CA, server name `loki.faas`, and cert and key from the existing `platform-logs` Promtail leaf path. The leaf keeps its directory, so `pkg/pki` and `platform_logs_pki` do not change; renaming it is not worth a PKI migration.
     - Promtail's bounded retry and backoff;
     - no write-ahead log, so a Loki outage costs the journal replay window, not local disk.
3. **Metrics and jobs stay where operators and automation expect them.**
   - Alloy's HTTP server listens on the address and port Promtail used (9080).
   - Pinned release: Alloy 1.20.1, published 2026-09-28. Its zip's SHA-256 matches both GitHub's asset digest and Grafana's `SHA256SUMS`. The newer 1.21 line was days old when this was written.
   - The Prometheus jobs keep the names `promtail` and `promtail-compute`, and apid's discovery path is unchanged. The names now mean "the log shipper"; renaming them would touch apid, `cd-controlplane.yml` and dashboards for no operator benefit.
   - Alert rules and the Loki pipeline dashboard move from `promtail_*` series to Alloy's (`loki.write` sent and dropped entries, and the journal source's read and parse counters). The exact names are checked against the pinned version's `/metrics` during implementation, and `TestAlertMetricsAreEmitted` requires the role files to name them.
   - During the transition the rules accept either family (`or`), so a mixed fleet stays covered.
4. **Budget and hardening.**
   - `alloy.service` runs in `faas-obs.slice` with `MemoryMax=256M`, `GOMEMLIMIT=200MiB` and `CPUWeight=50`.
   - It runs as an `alloy` system user in `systemd-journal`, reads its key through group `alloy` (granted by the role and by `platform_logs_pki`'s ownership pass, exactly as Promtail's is), and is added to the systemd hardening gate.
   - It is started with:
     - `--disable-reporting`: Alloy sends anonymous usage statistics by default, and the only outbound destination allowed is Loki;
     - `--stability.level=generally-available`, so the config cannot load experimental components.
   - Its HTTP server, which also serves a configuration UI, binds only to the private address (loopback on the control plane). The nftables rule that admits 9080 only from the control plane stays as it is.
5. **The measurement gate decides the fit.**
   - `scripts/ops/obs_gate.py` gains two checks:
     - the shipper's peak memory (`faas_cgroup_memory_anon_bytes{cgroup="faas-obs.slice/alloy.service"}`) at most 200 MB;
     - its average CPU at most 0.05 cores, over the §7 window on a non-production control plane.
   - If Alloy fails either check, Vector replaces it in the same role shape. Vector's Loki sink has no server-name override, so `loki.faas` then resolves through a managed `/etc/hosts` entry on each host.
   - Fluent Bit is not the fallback: it ships only as apt packages or a source build, and keeping the original line while promoting parsed fields to labels needs a fragile chain of filters.
6. **Cutover on each host:**
   1. stop Promtail;
   2. start Alloy with a first-run `max_age` of 10 minutes, so the gap is covered and at most 10 minutes of lines are sent twice. Loki drops duplicates with the same timestamp and line in the same stream while they are in the head block.
   3. On later starts Alloy resumes from its own positions.
   - Promtail's positions file is not converted: the formats differ, and 10 minutes of replay is cheaper than a converter.

## Consequences

- The shipper on every host receives security fixes again, from the same vendor as Loki and Grafana, under Apache-2.0.
- The translation is mechanical and keeps the ADR-143 label rule. The riskiest part of the pipeline, which fields become labels, is the same stage list in the same order.
- Alloy is heavier than Promtail. The gate measures it on the control plane before production, and the fallback is decided in advance.
- Alloy embeds OpenTelemetry Collector components. If ADR-20261023124955 §4's trace condition is met later, the same agent can receive OTLP from the daemons, rather than a new process family arriving. This ADR does not enable it.
- Alert, dashboard and Prometheus-job semantics are unchanged for operators. The metric names under them change, behind a transition window that accepts both.

## Rollout (small PRs)

1. The `alloy` role: pinned release and checksum, config template, unit, hardening gate, the unit-coverage test moved over, and a render test. Not selected by default.
2. Rules and dashboard accept Alloy or Promtail metrics; promtool fixtures for both.
3. `faas_obs_log_shipper` (default `alloy`) in `bootstrap.yml`. `role_convergence` stops and disables the shipper not selected, and the cutover step from Decision 6. `obs_gate.py` gains the shipper checks.
4. One release later: delete the `promtail` role and the `promtail_*` alternatives in the rules.

## Rejected alternatives

- **Keep Promtail:** unpatched, root-adjacent code on every host.
- **OpenTelemetry Collector:**
  - Its `journald` receiver shells out to `journalctl`, like Vector's source.
  - The Loki exporter is deprecated in favour of OTLP into Loki, which maps labels through resource attributes and Loki's own label configuration. That would change the ADR-143 label rule's mechanism, not just its config.
  - It would also put a large contrib binary on every host for one pipeline.
- **systemd-journal-upload:** sends journal files, not Loki streams; ADR-20261023124955 already rejected the `journal-remote` path.
- **Fluent Bit as the primary:** see Decision 5.

## References

- Grafana Loki documentation, "Promtail agent": end of life on 2026-03-02, with migration to Alloy — https://grafana.com/docs/loki/latest/send-data/promtail/
- Big Bang ADR 0004, "Alloy replacing Promtail": deprecation and long-term support from 2025-02-13, and `alloy convert --source-format=promtail` — https://docs-bigbang.dso.mil/latest/docs/adrs/0004-alloy-replacing-promtail/
- Grafana Alloy, "Estimate resource usage" — https://grafana.com/docs/alloy/v1.20/set-up/estimate-resource-usage.md
- Grafana Alloy, `loki.write` reference — https://grafana.com/docs/alloy/next/reference/components/loki.write
- Fluent Bit, Loki output (`tenant_id`, `label_keys`, `drop_single_key`) — https://docs.fluentbit.io/manual/pipeline/outputs/loki
- Vector, Loki sink reference — https://vector.dev/docs/reference/configuration/sinks/loki/
