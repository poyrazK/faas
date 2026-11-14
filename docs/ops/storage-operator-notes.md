# Storage: operator notes

Moved out of the customer page `docs/storage.md` (ADR-20261028200304).

## Data placement (ADR-098 / PR-A, inert)

Gregale places your compute close to where your data already
lives — Neon `us-east-1`, Upstash `eu-west`, your own API in
`ap-southeast`. The platform records the upstreams each app
talks to and biases wake-time placement toward the region
with the lowest observed RTT.

### Tables

- `data_upstreams` — one row per
  `(app_id, scope, kind, host, port)` the apid
  env-classifier has captured. Source = `inferred` (from
  env) or `explicit` (POST
  `/v1/apps/{slug}/upstreams`). Host stored plaintext for
  inspection; `host_redacted_hash` is
  `sha256(salt + host)` with `salt` from a deploy-time
  secret (ADR-098 D6). Migration:
  `migrations/00226_data_upstreams.sql`.
- `data_upstream_probes` — sliding 30s × 5min TCP+TLS probe
  samples (meterd's probe loop). PG15 declarative
  partitioning on `sampled_at` (monthly) with a default
  partition safety net. First partitioned table in the
  repo; PR-C adds the monthly
  `CREATE TABLE … PARTITION OF` cron.

### Owners

- `apid` — only writer to `data_upstreams`
  (env-classifier + customer surface).
- `meterd` — only writer to `data_upstream_probes`
  (probe loop + retention).
- `schedd` — reader of `data_upstream_probes` on wake
  (seeds `pkg/sched/upstream_affinity.go`).
- pg_notify channel `data_upstreams_changed` — schedd
  subscribes; payload
  `(app_id|scope|kind|host|port|op)` (pipe-delimited to
  fit under the 8000-byte pg_notify limit on a worst-case
  253-char host). Trigger defined in
  `migrations/00226_data_upstreams.sql`.

### Indexes

- `data_upstreams_app_created_idx` — per-app listing path
  (`GET /v1/apps/{slug}/upstreams`).
- `data_upstreams_host_redacted_idx` — host-region probe
  lookup.
- `data_upstreams_dedupe_uniq` — UNIQUE on
  `(app_id, scope, kind, host, port)` (ON CONFLICT
  tripwire).
- `data_upstream_probes` (partitioned) + default
  partition.

### Retention

- `data_upstreams` — no time-based purge; rows cascade
  on account / app delete (GDPR path: deleting an
  account cascades through apps → data_upstreams via FK
  ON DELETE CASCADE).
- `data_upstream_probes` — 30 days (matches §12
  `prom_retention_days:15` × 2 safety margin). Hourly
  cron calls
  `Store.PruneDataUpstreamProbesOlderThan(cutoff)`.
  PR-C's partition creator DROPs whole partitions for
  ranges entirely older than cutoff; this query handles
  the partial-partition tail.

### Defaults

Every PR-A flag is OFF (ADR-098 D7: `FAAS_DATA_PLACEMENT`,
`FAAS_UPSTREAM_PROBE`, `FAAS_UPSTREAM_AFFINITY`). PR-A's
only runtime effect is the table DDL + the
`data_upstreams_changed` pg_notify trigger (which fires
on writes but has no LISTEN subscriber until PR-B — pg_notify
drops unlistened payloads, so no backlog accumulates).
The customer surface, the env-classifier, the meterd probe
loop, and the schedd affinity read land in PR-B through
PR-D per `docs/adr/098-pr-cluster-outline.md`.

## Stateless advisory internals

The live advisory frame is published on the `db.NotifyStatelessAdvisory`
pg_notify channel (Wave 0 PR-C / ADR-047). The base-image deny-list lives in
`pkg/imaged/base.go`. The edge response cache is bounded, in-process and per
`gatewayd-internal`.
