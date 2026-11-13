# Storage

Apps on Gregale are stateless. Your code runs in an ephemeral microVM
that wakes, executes, parks, and forgets. Keep state in
[managed PostgreSQL](postgres.md) or in a provider you already use.

Managed object storage is in preview and may not be enabled for your account.
It does not add persistent VM disks or change runtime storage billing.

## Why stateless

Scale-to-zero economics is the load-bearing reason: an instance
that holds state would either need to stay warm forever (defeats
the model) or write state somewhere that survives a wake/park
cycle (adds a write-amplification layer that would undermine the
platform's isolation and scale-to-zero economics). MicroVMs are fungible — every wake boots from the
same snapshot, every park destroys local state — and snapshot
reuse only works because instances are interchangeable. Local
filesystem state is ephemeral by design; every wake is a fresh
boot.

## Ephemeral disk boundary

Each app's main `drive1` is a writable ext4 upper layer. Its capacity is
bounded by the plan's ephemeral disk ceiling, exposed as
`ephemeral_disk_max_mb` in account and app effective-limit responses. The
legacy `app_layer_max_mb` field remains for compatibility; both names refer to
the same physical cap. The value is total filesystem capacity, including the
application files and ext4 metadata; free scratch space is therefore the plan
capacity minus deployed content and filesystem overhead. Image builds enforce
the ceiling and provision that full logical capacity before a snapshot is
created. Unused blocks remain sparse on local SSDs and compressed in the remote
registry, so logical capacity does not consume an equal amount of physical
storage or network transfer.

Writes to `drive1` are rate limited per plan: 50 MB/s and 2,000
operations/s on Free, 100 MB/s and 4,000/s on Hobby, 200 MB/s and 8,000/s on
Pro and Scale, each after a one-time burst of 256 MiB and 20,000 operations.
The limit keeps one app's disk flood from slowing its neighbours.

`/tmp` is a separate tmpfs and is also lost when the instance parks. Sidecar
drives are read-only. Gregale does not attach durable customer volumes, and
the API currently reports the per-plan ceiling rather than live free-space
samples from a guest. Use object storage or an external database for state that
must survive a wake/park cycle.

Gregale rejects stateful base images when it accepts a deployment
(`stateless_only_violation`, HTTP 422): `postgres:16`, `mysql:8`, `redis:7`, `mongo:7`, and
the rest. Tarballs with `VOLUME` directives or top-level
`data/` / `db/` directories are rejected for the same reason.

## Recommended providers

Besides [managed PostgreSQL](postgres.md), Gregale works with the providers
you already use. Pick the category that matches the workload, plug in the URL,
and `gregale env set` the variables your code reads.

| Category        | Provider                              | Env vars |
|-----------------|---------------------------------------|----------|
| Object storage  | AWS S3, Cloudflare R2, Backblaze B2   | `S3_BUCKET`, `S3_REGION`, `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY`, optional `S3_ENDPOINT` (R2/B2) |
| Managed SQL     | Neon, Supabase, PlanetScale, CockroachDB Cloud | `DATABASE_URL` |
| KV / cache      | Upstash Redis, Upstash KV             | `UPSTASH_REDIS_REST_URL`, `UPSTASH_REDIS_REST_TOKEN` |
| Document        | MongoDB Atlas, Turso (libSQL)         | provider-specific |
| Queue / scheduler | Upstash QStash, AWS SQS             | `QSTASH_TOKEN`, etc. |

> **Note on the edge response cache.** Gregale also has an opt-in
> [response cache](cache.md), turned on with a `kind=cache` edge rule. It is
> **not** a KV replacement — off by default, no persistence, no sharing
> across nodes, no bodies at rest, no consistency across instances. It exists
> to avoid *wakes* for hot cacheable GET paths, not to give you
> multi-region cache. For everything else — durable cache, KV with TTL,
> pub/sub, sessions — bring your own from the table above.

## Wiring it up

`gregale env set` writes the value to sealed secrets at rest; at
wake time the runner injects every secret as a plain environment
variable inside the guest. No SDK lock-in, no special API surface,
no extra headers — `process.env.DATABASE_URL` is what your code
reads.

For a new app, use `gregale deploy --secrets-file <path>` (or
`gregale init --deploy --secrets-file <path>` for a template). Gregale
creates the app, seals the bundle, and starts the first deployment in
that order; run `gregale env set` only after the app exists.

For a monorepo project, the same bundle can be applied in one command with
`gregale deploy --project --secrets-file <path>`. Gregale seals the values
through the existing per-app secret path for every workload selected by the
project plan; use `gregale env set` afterward when one workload needs an
override.

```sh
gregale env set --app <slug> DATABASE_URL='postgres://user:pass@host/db?sslmode=require'
```

## Don't

- **Don't** add a `VOLUME` directive to your Dockerfile. The
  accept-time tarball scan rejects it.
- **Don't** run `postgres:16`, `mysql:8`, `redis:7`, `mongo:7`,
  `mariadb`, `cockroach`, `cassandra`, `clickhouse` as your base
  image. The deny-list rejects the deployment before the guest
  ever wakes.
- **Don't** write to database-shaped local paths
  (`/var/lib/postgresql`, `/data`, `/db`). Gregale flags these writes
  at runtime (see below).

## Detecting writes that will not survive

Gregale watches the state-shaped directories in every app instance:
`data/` and `db/` under the app's working directory (for a buildpack app,
`/app/data` and `/app/db`), plus `/data`, `/db`, `/var/lib/postgresql`,
`/var/lib/mysql`, `/var/lib/redis`, `/var/lib/mongodb`, `/var/lib/mongo`,
`/var/lib/cockroach`, `/var/lib/cassandra` and `/var/lib/clickhouse`.
The first write to each file in them records one `stateless.advisory`
audit row (writes within about a second share a row). The contract is
advisory-only by design (no read-only filesystem), so an app that *does*
try to persist state keeps running — the audit row is your signal that the
data will not survive the next park.

- **Querying the audit log:**
  `gregale audit list --kind-prefix stateless.advisory` lists recent rows.
  The dashboard's app-detail page links to the same view at
  `/dashboard/audit-events?kind_prefix=stateless.advisory&app_id={uuid}`.
  The HTTP API is
  `GET /v1/audit-events?kind_prefix=stateless.advisory&limit=50`.
- **Live events:** one summary frame per row
  (`{app_id, instance, n, sample_path}`) arrives on `GET /v1/events`
  within a few seconds of the write. Toggle on the CLI with
  `gregale invocation watch --include-stateless`.
- **Anonymous rows:** if the app was deleted between the wake and the
  advisory, no row is written on a compute node; on a single-box
  installation the row may land with `subject=NULL`. Pass
  `?include_anonymous=true` on the HTTP query, or `--include-anonymous`
  on `gregale audit-events`, to surface those rows.
- **Noise:** each file is reported once per instance, however often the
  app rewrites it; a new instance (the next wake) reports it again. An
  instance reports at most 256 files.

## Templates

`gregale init` scaffolds a working project that uses the right
provider. Each template fails clearly at startup (or in the first
invocation, for function handlers) if the secrets aren't set.

- `gregale init --template=s3-uploader` — port-8080 Node app, multipart
  upload to S3 / R2 / B2.
- `gregale init --template=slack-bot` — port-8080 Node app, Slack
  Events with HMAC-SHA256 signature verification.
- `gregale init --template=rest-api-postgres` — port-8080 Node app,
  Express + `pg` against a managed PostgreSQL.
- `gregale init --template=cron-worker` — exported handler for Upstash
  QStash invocations, with a Redis-backed progress counter.

See `gregale init --help` for the full flag surface and the
`--deploy` chain (materialize + deploy in one command).
