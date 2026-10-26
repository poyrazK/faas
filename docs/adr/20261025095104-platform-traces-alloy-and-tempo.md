# ADR-20261025095104: Platform traces — Alloy collects, Tempo on the control plane stores

- **Status:** proposed. The implementation can land switched off (`faas_obs_traces_enabled: false`). Enabling it in production waits for the measurement gate of ADR-20261023124955 §7, run with traces on.
- **Date:** 2026-10-25
- **Amends:**
  - [ADR-20261023124955](20261023124955-observability-on-the-control-plane.md) §4, "no trace backend in this phase". This ADR answers that section's revisit conditions.
  - [ADR-20261023185953](20261023185953-replace-promtail-with-alloy.md): Alloy gains an OTLP traces pipeline next to the journal one.
- **Related:**
  - [ADR-958](958-zero-config-guest-tracing.md): customer in-guest spans. They keep their own path and are out of scope.
  - [ADR-097](097-schedd-wake-phase-telemetry.md): wake-phase events.
  - ADR-1155: profiling.
  - `docs/ops/otlp-export.md`: an operator-chosen collector stays possible.
- **Decision in one line:** each daemon exports spans to the Alloy on its own host. Alloy forwards them to the control plane's Alloy over fleet-PKI mTLS, which tail-samples them and writes the kept traces to a single-binary Tempo in `faas-obs.slice`. Tempo keeps 7 days in the same object storage as Loki. Exemplars, logs, Grafana and `gregalectl obs wake` link to the trace.

## Context

**What already exists.**
- Spans in `pkg/gateway`, `pkg/sched`, `pkg/fcvm`, the vmmd and schedd gRPC paths (`pkg/wire/otelgrpc.go`), `pkg/outbound` and `pkg/dependencytrace`.
- `pkg/wire/otelinit` runs an OTLP/HTTP exporter in every daemon:
  - It is a no-op unless `OTEL_EXPORTER_OTLP_ENDPOINT` or `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` is set.
  - Its head sampler is `ParentBased(DeploymentAware(TraceIDRatioBased))`, rate 1.0 by default, and the first 100 roots of every deployment are always sampled.
  - It exports health metrics (`*_otel_trace_exporter_enabled`, `*_spans_exported_total`, `*_spans_dropped_total`, …) watched by `FaasOTLPTraceExporterDown` and `FaasOTLPTraceExporterErrors`.
- Exemplars carry `trace_id` on the gateway's request and wake histograms and on schedd's phase histograms.
- The gateway injects a W3C `traceparent` into every guest-bound request, so the customer debugger's `request_telemetry` rows (ADR-958) share the platform's trace IDs.
- Grafana's Prometheus datasource template already renders `exemplarTraceIdDestinations` when `gv_trace_datasource_uid` is set.
- Every unit loads the operator-owned `/etc/faas/otel.env` when it exists. That file also holds the metrics bridge settings and `FAAS_GUEST_TRACING_ENABLED`.

**What is missing.** Nothing collects or stores the spans. In production the exporter is off: no `faas_otel_trace_exporter_enabled` series, checked 2026-10-25.

**§4's revisit conditions.**
1. *An incident needed cross-daemon span timing that logs and wake-phase events could not give.* This has happened at least three times:
   - **The September 350 ms miss** (platform p95 2,084 ms). It was decomposed with a probe script over wake events and vmmd's restore breakdown. The resume-hook stall behind it stayed unexplained.
   - **The October restore regression.** A per-account pmem clone was re-copied on every wake (#5103). It was found with read-only `bpftrace` on a production vmmd.
   - **The first wake after a schedd restart.** It shows a 9.6 s gap between `wake.admitted` and `wake.boot_started`, inferred from event pairs. The cosign layer `Verify` is suspected and still unconfirmed.

   Each is a question about where time went inside or across daemons, which a span tree answers directly. **Met.**
2. *§7 shows the slice below half its limits for a week.* The gate has not run, because no host could be created (`gregale-prod` is at 12/12 CPUs and 250/250 GB SSD). The 2026-10-24 smoke measured `faas-obs.slice` at a 392 MB anonymous-memory peak of 3,584 MB and 0.094 cores, at 1000× production's log rate. **Not met**, so production enabling stays gated on it (§6 below).

**Volume.** Production over 24 h, read-only, 2026-10-25:
- about 930 gateway requests;
- 135 wakes, 23 of them over 1 s;
- 6 5xx responses;
- a peak of 0.32 req/s.

At about 10 spans per wake and 3 per warm request (estimates), that is roughly 4,000 spans a day, and keeping every trace costs tens of MB a week. Sampling exists for growth, not for today.

**Constraints.**
- Everything runs on the control plane (owner, 2026-10-23), which has 2 vCPU and 16 GB and is CPU-bound by PostgreSQL.
- Customer data must not leave by accident.
- The daemons stay vendor-neutral (`docs/ops/otlp-export.md`).

## Options

| | Option | Why not / why |
|---|---|---|
| A | Keep §4: no backend | Free, but keeps the investigations above manual. |
| **B** | **Alloy collects, Tempo on the control plane stores** | **Chosen.** Alloy already runs on every host, its OTLP and tail-sampling components are GA, Tempo stores blocks in the object storage Loki already uses, and Grafana links metrics, logs and traces natively. |
| C | Jaeger all-in-one | Local Badger storage on a control plane with no SSD quota left, or an extra Cassandra or Elasticsearch. No object-storage backend without more parts. |
| D | OpenTelemetry Collector contrib + Tempo | The same components as B, plus a second agent binary on every host next to Alloy. |
| E | Hosted (Grafana Cloud Traces, Honeycomb, Datadog) | A new subprocessor for span attributes (app and account IDs, destination hosts), and a recurring cost. ADR-20261023124955 kept logs in-house for the same reason. An operator who wants it can still point `/etc/faas/otel.env` at one. |

## Decision

### 1. Export: every daemon to its own host's Alloy

- Daemons export OTLP/HTTP to `http://127.0.0.1:4318`. This is plaintext over loopback, the case `pkg/wire/otelinit`'s security note allows, and no span crosses a host unencrypted.
- Only `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` is set. The metrics bridge stays off, and Prometheus remains the metrics path.
- Ansible renders this into a new `/etc/faas/otel-platform.env` (root:root 0444, no secrets). Every unit loads it *before* the operator-owned `/etc/faas/otel.env`, so an operator's file still wins. `FAAS_GUEST_TRACING_ENABLED` and any operator collector settings are untouched.
- Head sampling stays at the default rate of 1.0; the decision moves to the tail (§2). If volume outgrows the budget, `OTEL_TRACES_SAMPLER_ARG` in `otel-platform.env` lowers it without a code change.

### 2. Alloy: receive everywhere, decide on the control plane

**Every host's Alloy** (the `alloy` role) gains `otelcol.receiver.otlp` on `127.0.0.1:4318`.

**A compute node's Alloy**:
- runs `otelcol.processor.batch` and sends with `otelcol.exporter.otlp` to the control plane's Alloy at `faas_obs_loki_address:4317` (gRPC);
- uses TLS with the fleet CA and the platform-logs client leaf Alloy already holds (`/etc/faas/tls/promtail/loki-client.*`).

**The control plane's Alloy**:
- **Receives** on loopback HTTP from local daemons, and on `<private address>:4317` gRPC from compute nodes.
  - The private listener requires a client certificate from the fleet CA.
  - Its server leaf is a new control-plane role in the `platform-logs` PKI feature (`alloy/otlp-server`, server name `otlp.faas`), issued and renewed like the Loki leaf.
- Then runs, in order:
  1. **`otelcol.processor.memory_limiter`**: refuses data before Alloy runs out of memory.
  2. **`otelcol.processor.tail_sampling`.** All spans of a trace meet here, because there is one control plane.
     - `decision_wait` is 35 s, longer than the gateway's 30 s wake hold, so a queued wake's late spans still count.
     - `num_traces` is 50,000.
     - A trace is kept when it has any span:
       - with status ERROR;
       - lasting 1 s or longer;
       - carrying a `wake_id` attribute (every wake, 135 a day today);
     - or else with probability 10 %, tuned once real volume is measured.
  3. **`otelcol.processor.attributes`**: deletes `gregale.flag.value` (§5).
  4. **`otelcol.processor.batch`**, then **`otelcol.exporter.otlp`** to Tempo on `127.0.0.1:4417` over loopback plaintext.

**Component stability.** All of these are GA in the pinned Alloy 1.20.1: `receiver.otlp`, `processor.memory_limiter`, `processor.tail_sampling`, `processor.attributes`, `processor.batch` and `exporter.otlp`. So the role keeps `--stability.level=generally-available`.

**Memory.** The control-plane Alloy's limit rises from 256 MB to 384 MB for the tail-sampling buffer; compute nodes stay at 256 MB.

### 3. Tempo

- A new `tempo` role: a single binary (`-target=all`) pinned by SHA-256 to the newest Tempo 3.1.x release that is at least two weeks old.
  - Tempo 3's single-binary mode ingests without Kafka (3.0 release notes).
  - The implementation PR proves the rendered config by starting Tempo in the `obs-stack-smoke` CI job (§6).
- **Unit and limits.** It runs in `faas-obs.slice` as user `tempo` with `MemoryMax=512M` and `GOMEMLIMIT=400MiB`. The unit is hardened like Loki's, including an explicit instance address, because the address-family sandbox blocks interface discovery (the Loki fix of 2026-10-24).
- **Ports.** Loopback only, all checked free (the same check as `TestLokiPortsAreFree`):
  - HTTP `3200`, the query API for Grafana and `gregalectl`;
  - OTLP gRPC `4417`, for the control plane's Alloy;
  - internal gRPC `9198`.

  Tempo's default gRPC port, 9095, is Prometheus's.
- **Storage.** S3 to the same provider and bucket as Loki, under the prefix `tempo/`, with the same root-only credentials file pattern and optional `ca_file`. The WAL stays on local disk under `/var/lib/tempo`.
- **Retention.** 7 days (`block_retention: 168h`). Traces matter for recent incidents; logs keep 14 days.
- **Metrics generator: off.** Span metrics would duplicate the Prometheus histograms the platform already keeps and add cardinality.

### 4. Links

- **Grafana.**
  - A Tempo datasource (`uid: tempo`, loopback).
  - `gv_trace_datasource_uid: tempo`, so exemplars on any wake or request panel open the trace.
  - A Loki derived field that turns `trace_id` into a Tempo link.
  - Tempo's traces-to-logs, which goes back to Loki by `trace_id`.
- **`gregalectl obs wake <wake_id>`** prints the wake's trace IDs from its events. On the control plane it also fetches each trace from `127.0.0.1:3200` and prints the span tree under the timeline; `--no-trace` skips that.
- **`gregalectl obs trace <trace_id>`** prints one trace's span tree. It reads Tempo directly on the control plane, the way `obs logs` reads Loki.

### 5. What is stored, and what is not

- **Platform daemon spans only.**
  - Customer in-guest spans keep ADR-958's path (guest → vsock → vmmd → apid → `request_telemetry`) and never reach Tempo.
  - Guests cannot reach any receiver. On hosts they listen on loopback, and the control plane's private listener sits in RFC1918 space, which tenant egress denies (spec §11). It also requires a fleet client certificate.
- **Span attributes** are IDs (`app_id`, `deployment_id`, `instance_id`, `wake_id`, `gregale.internal.account_id`), outcomes and phase names.
  - Dependency and outbound spans add the destination host (`server.address`), method and status.
  - URL paths, queries, headers and bodies are never recorded.
  - A test pins the attribute keys used in platform code to a reviewed list, in the spirit of `TestAlertMetricsAreEmitted`, so a new key is a reviewed change.
- **`gregale.flag.value`** (a customer-set feature-flag value) is deleted in Alloy before storage.
- **Same posture as Loki:** an operator-only store, account and app IDs present, the same object-storage provider and bucket, and a shorter retention (7 days).

### 6. Gate, alerts and CI

- **§7's gate gains trace checks.** `obs_gate.py` adds them, and they SKIP when traces are off:
  - Tempo's anonymous-memory peak is at most 80 % of its `MemoryMax`;
  - the control plane's Alloy CPU stays within the §7 shipper budget;
  - Alloy's span export sent no failures;
  - the tail sampler dropped no trace before its decision.
- **Warning alerts**, with runbook `FaasTracePipeline`:
  - `FaasTracePipelineExportFailing`: Alloy fails to send spans to Tempo or the control plane;
  - `FaasTempoDown`;
  - `FaasTailSamplingOverflow`: traces dropped before decision.

  The existing `FaasOTLPTraceExporterDown` and `FaasOTLPTraceExporterErrors` keep watching each daemon's own exporter.
- **`obs-stack-smoke`** converges Tempo too. It sends a span to the local Alloy and finds it in Tempo by trace ID, through the same control-plane Alloy path production uses.

### 7. Switch and rollback

- `faas_obs_traces_enabled`, default false. It requires `faas_obs_logs_enabled`, because it shares Alloy, the PKI leaves and the bucket.
- **Off** means:
  - no `otel-platform.env`, so the daemons' exporters stay no-ops;
  - no OTLP pipeline in Alloy;
  - Tempo stopped and disabled by `role_convergence`.

  Setting it back to false is the rollback.

## Consequences

- **Positive.**
  - Span timing from the gateway through schedd to vmmd for every wake and for every slow or failed request.
  - Reachable from a slow histogram bucket (exemplar), a log line (`trace_id`), or `gregalectl obs wake`.
  - The three investigations above become one query each.
- **Cost on the control plane:**
  - Tempo, up to 512 MB;
  - +128 MB for the control plane's Alloy;
  - a few MB of objects a day today;
  - each daemon's batch export every 2 s.

  All of it is measured in the gate before production.
- **Risk.** The tail-sampling buffer grows with span rate × `decision_wait`. It is bounded by `num_traces`, the memory limiter and the unit's `MemoryMax`, and head sampling is the release valve.
- **Operator surface:**
  - a new role and three loopback ports;
  - one new firewall opening (compute → control plane 4317);
  - one new PKI server leaf;
  - a new env file every unit loads.

## Rollout (small PRs)

1. **PKI and Tempo:**
   - the `alloy/otlp-server` leaf in `pkg/pki` FeaturePlatformLogs;
   - the `tempo` role (switched off);
   - port and attribute-key tests.
2. **Export and collection:**
   - the Alloy OTLP pipeline behind `faas_obs_traces_enabled`;
   - nftables 4317 from compute to the control plane;
   - `otel-platform.env` and its `EnvironmentFile=` line in every unit.
3. **Links, alerts and checks:**
   - the Grafana datasource, exemplar destination and Loki derived field;
   - alerts and runbook;
   - `obs_gate.py` checks;
   - the `obs-stack-smoke` extension.
4. **CLI:** the trace section in `gregalectl obs wake`, and `gregalectl obs trace`.
5. **Enable:** in one environment, after the gate.

## References

- ADR-20261023124955 §4 and §7; ADR-20261023185953; ADR-958; ADR-097; `docs/ops/otlp-export.md`.
- Alloy 1.20.1 component docs: `otelcol.receiver.otlp`, `otelcol.processor.tail_sampling` (GA), `otelcol.processor.attributes`, `otelcol.processor.memory_limiter`, `otelcol.exporter.otlp`.
- Tempo 3.0 release notes: single-binary local ingest without Kafka.
- Production volume and exporter state: read-only queries on gregale-cp's Prometheus, 2026-10-25.
