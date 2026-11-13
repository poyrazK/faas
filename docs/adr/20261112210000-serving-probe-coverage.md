# ADR-20261112210000 · Serving probe coverage follows independent deployment intent

- **Status:** accepted for implementation; production rollout pending.
- **Date:** 2026-11-12
- **Builds on:** [response validation and evidence gates](20261112-monitoring-evidence-and-serving-validation.md).

A shared `faas_serving_probe_fleet` mapping supplies compute-node probe settings
and independent control-plane expectations. Stable registry node names and
exact URLs identify probes. Null means unconfigured; an explicit empty mapping
means zero expected probes. Standalone settings remain supported without
claiming coverage.

The Prometheus role includes `serving_probe_inventory`, which atomically renders
expected pairs and node timing to a control-plane node_exporter textfile.
Expected bodies and notification credentials are excluded. Intent outlives
compute execution and metrics, so a never-installed probe or deleted result
file remains missing. The inventory allows at most 1000 pairs, matching the
existing discovery target bound.

Recording rules match intent to `node-compute` scrape identities and assess
freshness, the probe's own run clock, unique evidence, content checks and
latency budgets. A healthy sibling cannot substitute for a missing node or
URL. Duplicate or missing metadata cannot establish health. Missing or
inconsistent inventory leaves coverage unknown. The provisioned fleet Grafana
dashboard uses instant queries to show current inventory status, expected
count, per-node coverage and per-pair state. Queries also reject recording
samples older than two minutes. Operator API and CLI contracts
remain unchanged.

Warnings cover sustained coverage faults and an unavailable inventory source.
Direct probe notifications and external heartbeats retain their independent
control-plane outage path.

Connection-free Ansible tests render independent intent and node settings and
reject invalid policies. Promtool cases cover missing or vanished results,
wrong node and URL identities, frozen textfiles, future clocks, disabled
checks, budget drift, duplicates, missing metadata, partial inventory and
recovery. CI verifies rules and the dashboard mirror.

Publish intent on the control plane before converging compute nodes with the
same policy, then qualify the next run and warning recovery. Production
activation remains an operational step. See the
[setup and retirement procedure](../ops/serving-probe.md#coverage-from-deployment-intent).
