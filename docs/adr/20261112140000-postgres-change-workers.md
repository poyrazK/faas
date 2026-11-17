# ADR-20261112140000: Workers consume Postgres row changes through the Commit outbox

- Status: accepted (owner decisions 2026-11-12), built dark behind the
  `postgres_changes` platform flag
- Amends: ADR-430 (managed PostgreSQL Commit outbox), ADR-589 (Commit routing)
- Related: ADR-20261018101500 (worker pools), realtime changefeeds
  (`pkg/commit/realtime_changefeeds.sql`), data pipelines (`WithPipelineSQL`)

## Context

A common reason to run a worker is "do something when this row changes": send
the email when an order ships, sync a customer to Stripe, rebuild a search
document. Today a Gregale customer has to write that event themselves (dual
writes, or Gregale Commit's `insertCommitEvent` in every code path) or run
Debezium next to their database.

The platform already has both halves:

- **Capture.** Realtime changefeeds install an `AFTER INSERT OR UPDATE OR
  DELETE` row trigger that writes an allowlisted before/after image into a
  customer-owned outbox, in the writer's transaction.
- **Relay.** Gregale Commit (`pkg/commit`, `pkg/commitmanaged`) leases committed
  rows from `public.gregale_outbox` over a sealed, TLS-verified connection,
  deduplicates them by event id into `commit_receipts`, and enqueues each as an
  invocation of a fixed app, at least once.

What is missing: changefeeds write only to the realtime outbox; Commit refuses
worker apps (`destination_unqualified`, because workers serve no request
invocations); every install is a manual owner action; and the relay's network
policy is an operator host allowlist, so a customer cannot bring their own
database.

## Decision

### 1. Pipeline

```
customer table ──row trigger──▶ public.gregale_outbox ──Commit relay (schedd)──▶
  commit_receipts + invocation in the worker's named queue ──pull lease──▶ worker
```

A **changes source** is a Commit source (`commit_sources.kind = 'changes'`)
with a destination queue name. Nothing else in the relay changes: leases,
fencing, receipts, blocked events, replay and cleanup are Commit's.

### 2. Capture SQL

`pkg/commit/changes.sql` adds `gregale_changes_capture()`,
`gregale_changes_install_feed(table, config)` and
`gregale_changes_remove_feed(table, name)`. It follows the realtime changefeed
rules: ordinary tables with a primary key only, every emitted column (and every
key column) explicitly allowlisted, identifiers and literals quoted with
`format(%I/%L)`, `search_path` pinned to `pg_catalog` with every customer
object schema-qualified. Operations can be narrowed (`insert`, `update`,
`delete`); an update that changes no allowlisted column emits nothing.
`TRUNCATE` is not captured.

Each change becomes one outbox row:

- `event_type`: `database.<feed>.<operation>`, e.g. `database.orders.update`
- `payload`: `{"feed","table","operation","key","before","after"}`

Unlike the realtime changefeed function, the capture function is `SECURITY
DEFINER`, owned by the installing role. With invoker rights every role that
writes a tracked table would need `INSERT` on `gregale_outbox`, and a role
created after install (a new managed binding, say) would fail every write to
the table: capture must never break the customer's writes. A trigger function
cannot be called directly, its arguments are fixed by the installer, and its
pinned `search_path` plus qualified names leave nothing for a caller to
redirect. No customer role needs a grant on the outbox, so no customer role can
forge a change event.

### 3. Delivery into a worker queue

The Commit acceptor, for a `changes` source, enqueues an `InvocationQueue` row
with the source's `queue_name` into the app's **enabled, shared** queue binding
of that name, under the same per-app pending and per-account backlog-byte
admission as `queues/send`. The worker consumes it with the existing pull
leases (`consumeQueue` in the SDKs): retries, dead letters (`gregale dlq`),
`queue_depth` autoscaling and scale-to-zero all apply unchanged. The payload is
the normalized CloudEvents envelope Commit already builds (`source:
gregale.commit.<source>`, `type: database.<feed>.<op>`, `id`: the outbox
event id, stable across redeliveries).

A missing or disabled binding is a **retryable** acceptance failure, not a
blocked event: changes wait in the customer's outbox until the worker that
declares the binding is deployed. Environment-scoped bindings are out of scope
for this ADR.

Non-worker destinations keep today's behavior.

### 4. Who installs the SQL

- **Managed PostgreSQL: Gregale installs it.** On deploy (or
  `PUT /v1/apps/{slug}/postgres-changes`), apid runs the install through the
  existing `WithPipelineSQL` owner path, as the customer schema owner: the
  Commit schema, the source binding, the capture functions, the declared feeds
  (undeclared `gregale_changes_*` triggers are dropped). This relaxes ADR-430's
  "the platform never installs DDL" for managed databases only, where Gregale
  already owns the role model.
- **BYO PostgreSQL: the customer installs it.** `gregale postgres-changes
  install --connection-url-env <VAR>` (or `gregale deploy` when the
  manifest's `connection_url_env` is set locally) runs the same Go install
  routine on the customer's machine and uploads only the relay login's URL,
  before committing the install. apid refuses any URL that does not log in
  as the source's relay role. Gregale never holds the owner credential of a
  BYO database.

### 5. The relay's credential is least-privilege in both cases

The install creates (or rotates) a dedicated login role
`gregale_changes_<source>` with a random 32-byte password and only `USAGE` on
`public`, `SELECT` and `DELETE` on `gregale_outbox`, `UPDATE` on its delivery
columns only (lease, attempt and acceptance columns, never `event_type` or
`payload`), and `SELECT` on `gregale_commit_binding`. Its `sslmode=verify-full` URL is sealed to the
delivery domain with the existing Commit credential sealing
(`PUT /v1/commit-sources/{id}/connection`). The owner credential is never
stored. Re-running install rotates the password.

### 6. Network policy for customer databases

The relay's operator host allowlist cannot serve self-serve BYO. A new
operator switch `FAAS_COMMIT_PUBLIC_DATABASES=true` lets a `changes` source
connect to any host name whose **every** resolved address is public global
unicast (no private, loopback, link-local, CGNAT, unique-local, multicast,
unspecified, documentation, or cloud metadata ranges), re-checked on every
dial, with TLS verifying the original host name. The allowlist still governs
other Commit sources. BYO databases must present a publicly trusted
certificate; a customer CA is follow-up work.

### 7. Polling cost

The relay ticks every source every 5 seconds, which keeps a scale-to-zero
database (Neon suspends after 5 idle minutes) awake forever. A changes source
has `poll_interval` (default 5 s, 1 s to 10 min); the relay skips a source
until it is due. Docs state the trade-off: an interval of 5 minutes or more
lets an idle managed database suspend, at the price of delivery latency.

### 8. Shape in `gregale.yaml`

```yaml
worker:
  scale: { min: 0, max: 3, metric: queue_depth, target: 100 }
queue_bindings:
  - name: order-changes
    queue_name: order-changes
postgres_changes:
  database: shop                 # managed database; or connection_url_env for BYO
  queue_name: order-changes      # must be one of this app's queue_bindings
  poll_interval: 5s
  feeds:
    - name: orders
      table: public.orders
      columns: [id, status, total_cents]
      operations: [insert, update, delete]
```

One `postgres_changes` block per app; a database binds to exactly one changes
source (`gregale_commit_binding` is a singleton), so two apps cannot consume
the same database. Up to 20 feeds per source.

### 9. Guarantees stated to customers

At least once; a change is captured only if its transaction commits; no
ordering guarantee across rows or redeliveries (use the key and a version
column); `before` is the allowlisted image before the statement, not the
committed history of a row; deletes carry `before` only. No backfill: capture
starts when the feed is installed.

### 10. Release

Dark: the `postgres_changes` platform flag (claiming the `postgres-changes`
preview capability, so the routes stay out of the published SDKs) gates the
API; a deploy for an unreleased account warns and skips the block, and the
CLI command is out of customer discovery. schedd also needs
`FAAS_COMMIT_RELAY_ENABLED` (and `FAAS_COMMIT_PUBLIC_DATABASES` for BYO). Plans: Hobby,
Pro and Scale (workers are paid-only). GA additionally waits for Commit's
open production qualification items (docs/gregale-commit.md).

## Alternatives rejected

- **Logical replication (pgoutput).** Lowest latency and no write
  amplification, but needs per-project enablement on Neon, slots that pile WAL
  when abandoned, a held connection that blocks suspend, and is often
  unavailable on customers' hosted databases.
- **LISTEN/NOTIFY.** Not durable; 8 KB payloads.
- **A new `postgres_changes` trigger kind with its own poller.** Duplicates
  Commit's leases, fencing, receipts and credential sealing.
- **Routing through realtime channels.** Needs a realtime endpoint per feed
  and inherits message TTLs that do not fit queues.

## Consequences

- Writes to a tracked table cost one extra row insert in the same transaction,
  and accepted rows are deleted after Commit's retention (7 days by default).
- Customers get row-change events without dual writes or extra infrastructure,
  on managed and BYO Postgres.
- Commit's relay becomes customer-reachable for the first time; its
  qualification items move onto the GA path of this feature.
