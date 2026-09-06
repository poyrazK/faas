# Storage on this platform

This platform is stateless. Your code runs in an ephemeral microVM
that wakes, executes, parks, and forgets. Bring your own state.

An opt-in [customer object-storage preview](object-storage.md) now lets Gregale
manage private buckets on external S3 services (ADR-147). This does not add
persistent VM disks or change runtime storage billing. Bring-your-own providers
remain supported; native customer S3 keys are not part of the preview.

## Why stateless

Scale-to-zero economics is the load-bearing reason: an instance
that holds state would either need to stay warm forever (defeats
the model) or write state somewhere that survives a wake/park
cycle (adds a write-amplification layer we can't afford on a
one-box build). MicroVMs are fungible — every wake boots from the
same snapshot, every park destroys local state — and snapshot
reuse only works because instances are interchangeable. Local
filesystem state is ephemeral by design; every wake is a fresh
boot.

## Ephemeral disk boundary

Each app's main `drive1` is a writable ext4 upper layer. Its capacity is
bounded by the plan's ephemeral disk ceiling, exposed as
`ephemeral_disk_max_mb` in account and app effective-limit responses. The
legacy `app_layer_max_mb` field remains for compatibility; both names refer to
the same physical cap. Image builds enforce the ceiling before a snapshot is
created, so a deployment cannot boot with a larger writable app layer than the
plan allows.

`/tmp` is a separate tmpfs and is also lost when the instance parks. Sidecar
drives are read-only. Gregale does not attach durable customer volumes, and
the API currently reports the per-plan ceiling rather than live free-space
samples from a guest. Use object storage or an external database for state that
must survive a wake/park cycle.

The platform's deny-list (see `pkg/imaged/base.go` and the
`stateless_only_violation` 422) rejects stateful base images at
accept time: `postgres:16`, `mysql:8`, `redis:7`, `mongo:7`, and
the rest. Tarballs with `VOLUME` directives or top-level
`data/` / `db/` directories are rejected for the same reason.

## Recommended providers

The platform doesn't ship a managed-storage product; it integrates
with the providers customers already use. Pick the category that
matches the workload, plug in the URL, and `faas secrets set` the
env vars the runtime injects at wake.

| Category        | Provider                              | Env vars |
|-----------------|---------------------------------------|----------|
| Object storage  | AWS S3, Cloudflare R2, Backblaze B2   | `S3_BUCKET`, `S3_REGION`, `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY`, optional `S3_ENDPOINT` (R2/B2) |
| Managed SQL     | Neon, Supabase, PlanetScale, CockroachDB Cloud | `DATABASE_URL` |
| KV / cache      | Upstash Redis, Upstash KV             | `UPSTASH_REDIS_REST_URL`, `UPSTASH_REDIS_REST_TOKEN` |
| Document        | MongoDB Atlas, Turso (libSQL)         | provider-specific |
| Queue / scheduler | Upstash QStash, AWS SQS             | `QSTASH_TOKEN`, etc. |

> **Note on platform-owned edge response cache (ADR-122).** Gregale
> also ships a *bounded, in-process, per-`gatewayd-internal`* response
> cache gated on a per-app `kind=cache` edge rule. It is **not** a KV
> replacement — opt-in only, default-off, no persistence, no cross-node
> sharing, no bodies at rest, no cross-instance consistency. It exists
> to avoid *wakes* for hot cacheable GET paths, not to give you
> multi-region cache. For everything else — durable cache, KV with TTL,
> pub/sub, sessions — bring your own from the table above.

## Wiring it up

`faas secrets set` writes the value to sealed secrets at rest; at
wake time the runner injects every secret as a plain environment
variable inside the guest. No SDK lock-in, no special API surface,
no extra headers — `process.env.DATABASE_URL` is what your code
reads.

```sh
faas secrets set --app <slug> DATABASE_URL='postgres://user:pass@host/db?sslmode=require'
```

## Don't

- **Don't** add a `VOLUME` directive to your Dockerfile. The
  accept-time tarball scan rejects it.
- **Don't** run `postgres:16`, `mysql:8`, `redis:7`, `mongo:7`,
  `mariadb`, `cockroach`, `cassandra`, `clickhouse` as your base
  image. The deny-list rejects the deployment before the guest
  ever wakes.
- **Don't** write to database-shaped local paths
  (`/var/lib/postgresql`, `/data`, `/db`). The guest-init advisory
  (Wave 0 PR-C / ADR-047, shipped) flags these writes at runtime.

## Operational notes

Once the stateless advisory is in (Wave 0 PR-C / ADR-047), every
write to a state-shaped path emits one `stateless.advisory` audit
row per debounced 1-second batch. The contract is advisory-only by
design (no EROFS), so a customer app that *does* try to persist
state will continue to run — the audit row is the operator's only
signal that the data will not survive the next park.

- **Querying the audit log:**
  `faas audit-events --kind-prefix=stateless.advisory` lists
  recent batches. The dashboard's app-detail page links to the
  same view at `/dashboard/audit-events?kind_prefix=
  stateless.advisory&app_id={uuid}`. The HTTP API is
  `GET /v1/audit-events?kind_prefix=stateless.advisory&limit=50`.
- **Live SSE:** the `db.NotifyStatelessAdvisory` pg_notify channel
  fires one summary frame per batch
  (`{app_id, instance, n, sample_path}`) on
  `GET /v1/events`. Operators see a frame within ~2s of the
  guest write. Toggle on the CLI with
  `faas tail --include-stateless`.
- **Anonymous rows:** if the app row was deleted between wake and
  advisory, the row lands with `subject=NULL`. Pass
  `?include_anonymous=true` on the HTTP query, or
  `--include-anonymous` on `faas audit-events`, to surface
  those rows.
- **Noise:** a noisy app that writes `/data` on every request
  produces one advisory row per second. ADR-035's "audit rows
  are observation, not source of truth" already covers the
  noise; the dashboard UI dedupes repeat rows at `+1 more`.

## Templates

`faas init` scaffolds a working project that uses the right
provider. Each template fails clearly at startup (or in the first
invocation, for function handlers) if the secrets aren't set.

- `faas init --template=s3-uploader` — port-8080 Node app, multipart
  upload to S3 / R2 / B2.
- `faas init --template=slack-bot` — port-8080 Node app, Slack
  Events with HMAC-SHA256 signature verification.
- `faas init --template=rest-api-postgres` — port-8080 Node app,
  Express + `pg` against a managed PostgreSQL.
- `faas init --template=cron-worker` — exported handler for Upstash
  QStash invocations, with a Redis-backed progress counter.

See `faas init --help` for the full flag surface and the
`--deploy` chain (materialize + deploy in one command).

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
