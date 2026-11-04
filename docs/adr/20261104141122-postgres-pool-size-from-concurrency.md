# ADR-20261104141122: Postgres pools are sized from the instance's concurrency

- Status: proposed
- Date: 2026-11-04
- Related: ADR-1369 (PostgreSQL pool runtime bindings), ADR-20261022203523
  (per-instance concurrency), ADR-1027 (native managed PostgreSQL and its
  transaction pooler), ADR-20261023115741 (platform flags), spec §6.2-1

## Context

An app's database pool size is a number the customer guesses. The drivers'
defaults (`pg.Pool` 10, a psycopg pool's `max_size` chosen by the app, Go's
`database/sql` unbounded) know nothing about how much work one instance does.
Gregale does know: one instance serves at most its per-instance concurrency
(`scaling_policy.per_instance_concurrency`, else the plan's
`concurrency_per_vm`: Free 4, Hobby 5, Pro 25, Scale 80), and the app runs at
most `max_concurrency` + `RolloutConcurrencyGrant` instances (invariant 1).

- A pool larger than the instance's concurrency holds connections no request
  can use. Behind the transaction pooler they cost the pooler's client slots;
  on a direct or replica connection each one is a PostgreSQL session.
- A pool smaller than it makes requests wait for a connection while the
  instance has room for them.
- An unbounded Go pool opens one session per in-flight query.

## Decision

### 1. Gregale tells the app its pool size

imaged stamps `GREGALE_POSTGRES_POOL_MAX` (`api.PostgresPoolMaxEnv`) on every
deployment it builds for an account the `postgres_pool_sizing` platform flag
releases:

```
GREGALE_POSTGRES_POOL_MAX = min(per-instance concurrency, api.PostgresPoolMaxPerInstance)
```

`api.PostgresPoolMaxPerInstance` is 10, the `pg.Pool` default: no
instance's pool grows past what most apps run with today, and Free and Hobby
instances (4 and 5) stop holding connections they cannot use. The app as a
whole then opens at most
`(max_concurrency + RolloutConcurrencyGrant) × GREGALE_POSTGRES_POOL_MAX`
connections. A customer value already in the app's environment wins.

The value is fixed when the deployment is built, like `FAAS_FUNCTION_CONCURRENCY`
(ADR-1342): it is baked into the snapshot, and a changed plan or scaling
policy takes effect on the next deploy. It is not a secret and is not part of
a binding's secret snapshot, so rotating a binding never changes it.

### 2. The SDKs use it as the default

`PostgresPoolBinding` reads the variable when it builds a pool's configuration
and hands it to the factory next to the connection URI:

| SDK | Field | Unset |
|---|---|---|
| Node | `max` (the `pg.Pool` option, so `new pg.Pool({...config})` uses it) | absent |
| Go | `PostgresPoolConfig.MaxConnections` (for `db.SetMaxOpenConns`) | 0 |
| Python (sync and async) | `PostgresPoolConfig.max_size` (for psycopg's `max_size`) | `None` |

A missing, non-numeric or out-of-range value (outside 1–1000) is ignored, so
the factory behaves exactly as before. The SDK never sizes the pool itself:
the factory still chooses, and an explicit setting in it wins.

### Rollout

The `postgres_pool_sizing` platform flag (default off, consumer imaged)
releases the stamp per account, plan or organization. Off, deployments build
exactly as before and the SDK field stays unset. The SDK change ships first
and is inert until the flag is released. Customer docs
(`docs/postgres-pool-runtime.md`) describe the variable now, as a value that
may be present.

## Consequences

- Without a choice by the customer, a pool matches the work its instance can
  do, and the app's total connections follow from numbers the customer
  already set.
- imaged now always runs the platform-flag watcher, not only when on-demand
  profiling is installed. It is one runtime-config listener; a failed first
  reconcile is logged and the watcher retries.
- Release compatibility: the variable is new, older SDKs ignore it, and older
  imaged never stamps it.

## Steps

1. This ADR, the stamp, the flag and the SDK fields (one PR).
2. Release the flag to the owner's account; compare pool waits and server
   connections on production apps.
3. Release to every account; the starters drop their hard-coded pool sizes.
4. `gregale app binding verify --postgres` reports the app's connection total
   against the database's qualified connection allocation, where one exists.

## Alternatives considered

- **Divide a database budget by the instance count.** The budget is not known
  for most databases: behind the transaction pooler client connections are
  not sessions, and Neon's pooler limits depend on the compute size. The
  qualified connection allocation exists only for Data API connection
  budgets. Step 4 uses it where it exists instead of guessing elsewhere.
- **Put the size in the connection URI.** libpq rejects unknown URI
  parameters, so psycopg apps would fail to connect.
- **A per-binding key next to the URI.** Every binding of an app runs in the
  same instance with the same concurrency, so one app-wide value says the
  same thing without touching the secret store or rotation.
