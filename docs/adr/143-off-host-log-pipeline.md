# ADR-143 · Off-host platform log pipeline

- **Status:** accepted
- **Date:** 2026-09-05
- **Deciders:** Gregale platform team
- **Related:** issue #274, ADR-129

## Context

The platform's daemon logs and the structured records emitted while serving
customer app-log streams are valuable during a host failure. Keeping only the
local journald copy loses that evidence at exactly the time operators need it.
The path must also be asynchronous: a Loki outage cannot add latency or a new
failure mode to customer requests.

## Decision

Run a pinned single-binary Loki instance on a dedicated host in the
`observability` inventory group. Run a pinned Promtail instance on every
control-plane and compute host. Promtail reads an explicit allowlist of
systemd units from journald, persists positions under `/var/lib/promtail`,
and sends bounded batches over authenticated TLS with client certificates.

The local journal remains the short-lived replay source. Promtail may retry
for a bounded period and then drops entries with a Prometheus-visible counter;
it never blocks a serving request. Loki uses filesystem TSDB storage with a
seven-day default retention and explicit ingestion/query limits.

Customer app-log records are emitted as structured JSON by the authenticated
gateway log stream. Only the validated `account_id` and `app_id` fields are
eligible for Loki labels, together with fixed operational labels. Instance
IDs, request context, deployment filters, and log lines remain structured log
content, not labels. Control characters are sanitized before the record
reaches journald.

Prometheus discovers Promtail metrics from the active compute-node registry,
so node replacement and drain state do not require a static inventory edit.
`loki_send_total`, dropped entries, source journal lines, and shipper scrape
health are exposed through recording rules, alerts, and a Grafana dashboard.

## Consequences

- A compute/control-plane host loss no longer removes the only copy of its
  recent platform logs.
- The journal and Promtail positions file provide bounded restart replay; the
  Loki backend is not an archival or billing source.
- A Loki outage is visible and bounded rather than request-path blocking, but
  entries can be lost after the local journal and retry windows expire.
- Dedicated-host TLS material and private routing remain provider-owned
  deployment inputs; the repository stores paths and validation rules, never
  secret contents.
- The current customer-log source is the live app-log stream's structured
  records. Independent shipping of every VM ring line without a live consumer
  remains a separate follow-up.

## Rejected alternatives

- **Run Loki on the control plane:** a control-plane failure would remove the
  service and its incident evidence together.
- **Ship directly from request handlers to Loki:** network or backend failure
  would couple customer latency and availability to observability.
- **Use arbitrary JSON fields as labels:** customer-controlled values would
  create unbounded Loki stream cardinality and make retention unpredictable.
- **Use a static Promtail target list:** node replacement and drain would make
  the metrics view stale unless every deployment also edited Ansible.
