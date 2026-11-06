# ADR-20261106105015: gregale.yaml creates what it binds, `postgres connect`, and SDK lifecycle hooks

- **Status:** accepted
- **Date:** 2026-11-06
- **Amends:** ADR-20261025111824 (managed databases a project deploy provides now include gregale.yaml `create` entries); the "bucket creation stays explicit" rule in `pkg/gregalemanifest` (`BucketDependency`) and `cmd/gregale/commands_object_storage_manifest.go`
- **Context:** The ChronoLake field report (a four-service project with PostgreSQL and two buckets) asked for declarative resources, a database tunnel and runtime helpers. Most of the rest of that wishlist already shipped: queues, bucket notifications, `gregale logs --project`, `gregale debug trace`, `gregale dev bridge`. The gaps it hit:
  1. `databases:` in gregale.yaml binds only databases that exist and are ready, and `buckets:` only buckets that exist. A project deploy ignored root `buckets:` entirely, without a word. `--create-only` refuses project deploys, so a new project had to deploy once, create resources by hand, bind them and deploy again.
  2. Managed credentials exist only sealed in a bound app's environment, so reading the database from a laptop meant writing an HTTP endpoint or hand-running `app port-forward` with a credential copied out of `app exec`.
  3. Every service hand-wrote the `before_checkpoint`/`after_restore` endpoints: header and loopback checks, the 2-second limit, and dropping connections that went stale while parked or frozen.

## Decision

### 1. `create` on gregale.yaml databases and buckets

A `databases[]` or `buckets[]` entry may say `create: true`, or give an object:
`postgres_major`, `class`, `region` and `scale_to_zero` for a database, and
`region` for a bucket. The deploy creates the resource **only when none with that
name exists**: an existing one keeps its settings, and a deploy never changes or
deletes one.

- **Validation:** the manifest checks the name (a name, not an ID, in the API's
  name rule), the class and the major. A database declares `create` once; other
  entries for the same name bind the database it creates. `create: false` and
  unknown keys are errors.
- **Project deploys (apid):** the plan resolves everything before mutation, as
  for existing databases. A missing database with `create` gets the same gates
  as `gregale postgres create` (plan allows databases, provisioning open, the
  plan's database limit including other creates in the same plan, class/major
  offered, organization `postgres:manage`). The plan lists it in `managed` with
  `source: gregale.yaml` and a `provision`. Apply creates it (idempotent by
  name and spec) before reserving its bindings. A binding to a database that
  is still provisioning is accepted for a declared `create`, and the binding
  reconciler injects the key when the database is ready, as for Compose
  postgres services. A database left behind by a failed apply is reused by
  name on retry; data is never deleted implicitly.
- **Project buckets (apid):** root `buckets:` now apply to project deploys,
  matched to services by name or allocated slug. The plan checks
  `storage:manage`, object-storage availability, the async-invoke plan gate
  for triggers, and that a bucket without `create` exists on an existing app.
  It lists each under `managed` with `kind: bucket`. Apply creates missing
  buckets and binds them after the apps exist and before builds
  (`object.EnsureManifestBuckets`). It reuses an active binding with the same
  (app, scope, prefix), for example one made by `gregale add bucket`, and
  mints new ones under a stable ID per (bucket, app, prefix), so a retried
  apply does not mint twice.
- **Single-app deploys (CLI):** `gregale deploy` creates missing declared
  databases (in the capabilities' default region unless `region` is set), waits
  up to three minutes for them to become ready, then binds. It also creates
  missing declared buckets before binding. A missing resource without `create`
  still fails before any mutation, now with a hint to add `create: true`.
- `--create-only` still refuses project deploys. Its error now points at
  `create: true`, which removes the reason to bootstrap with empty apps.

### 2. SDK lifecycle hooks

`faas.LifecycleHooks` (Go), `@gregale/sdk-node/lifecycle/runtime` and
`faas_sdk.lifecycle_runtime` serve both hooks. Each answers only loopback
`POST`s that carry `X-Faas-Before-Checkpoint: 1` / `X-Faas-After-Restore: 1`,
runs registered callbacks in order under a 1.5 s budget (below the 2 s limit),
returns 204 or 500, and shares one in-flight run between overlapping
deliveries. Pool helpers close idle connections on both hooks:
`RecycleIdleConnections(*sql.DB, n)`, `recycle_sqlalchemy(engine)` and
`expire_asyncpg(pool)`. They **do not reconnect inside `after_restore`**: a SQL
reconnect there missed the 2 s limit on n2 nested virtualization, turning
restores into cold boots. Closing stale sockets is local, and pools dial on
demand. Secrets stay with `SecretReloadCoordinator`; the helpers do not
reload them.

### 3. `gregale postgres connect`

`gregale postgres connect <database>` picks a ready binding the caller can
see (preferring `read_write`, then `migration`, then `read_only`; `--app`, `--env`
and `--access` choose). It reads the binding's value with `printenv KEY` in a
task VM of the bound app, relays a free local port to the database host with
the existing port-forward built-in (ADR-1037), and runs local `psql` with
`PGHOST` set to the original host and `PGHOSTADDR` to the tunnel. TLS
verification and SNI-routed providers keep working, and the password reaches
`psql` only through `PGPASSWORD`. `--proxy` keeps the tunnel open for another
client. It is part of the `app-tasks` capability.

## Consequences

- A new multi-service project with databases and buckets deploys in one
  reviewed apply.
- Removing an entry, or `create`, never deletes anything.
- Older CLIs print gregale.yaml buckets in the plan under the managed
  PostgreSQL heading. The fields are additive, and the apply is the same.
- `postgres connect` needs `printenv` in the image (Debian, Ubuntu and Alpine
  have it) and starts two task VMs per session.
- Not done here: a local Docker emulator for `gregale dev`. `gregale dev`
  (remote development environments with `--postgres`/`--bucket`) and `gregale
  dev bridge` cover running code against the platform. A local emulator would
  be a second implementation of the mesh, secrets and snapshot contracts to
  keep in step.

## Rejected alternatives

- **A new `resources:` block in compose.yaml.** gregale.yaml already names
  databases and buckets per binding. A second place to declare them would need
  merge rules, and Compose `postgres` services already map to managed
  databases.
- **Reveal the connection string through the API for `postgres connect`.** A
  new credential-reading endpoint is a larger surface than reusing app tasks.
  Anyone who can run app tasks can already read the app's environment.
- **Reconnect pools in `after_restore`.** See §2; it trades a stale socket for a
  cold boot.
