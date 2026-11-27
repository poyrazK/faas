# Migrate from Render

A Render web service usually moves to Gregale without code changes: both
build from source or a Dockerfile, set `PORT`, and pass configuration as
environment variables. Gregale does not read `render.yaml`; this page maps
its pieces to Gregale's and walks through a move.

## How Render maps to Gregale

| Render | Gregale |
| --- | --- |
| Web service | App, deployed with `gregale deploy` |
| Instance | Instance: a Firecracker microVM per instance. Idle apps park and wake on the next request. |
| Native runtime build and start commands | Source builds detect Node, Python and Go and infer the start command ([Deploy a web framework](frameworks.md)); pin it with `hosting.start` in `gregale.yaml` |
| Docker runtime | Your `Dockerfile`, as is ([Container compatibility](container-compatibility.md)) |
| Pre-deploy command | A [release command](releases.md#releases-that-change-the-database): `release.command` in `gregale.yaml` or `release:` in a `Procfile` |
| Health check path | `hosting.health` in `gregale.yaml` |
| Private service | A service in the same [project](multi-service-apps.md), called by name |
| Background worker | A background worker service in a [project](multi-service-apps.md): a service without `ports:` runs all the time |
| Cron job | A [cron](crons.md) that calls a path on your app, or a scheduled [job](jobs.md) (`gregale job create --image ... --schedule`) |
| Environment variables and secret files | [Secrets](secrets.md) (`gregale env set`) and [environment variables](env.md) |
| Render Postgres | [Managed PostgreSQL](postgres.md), bound to the app as `DATABASE_URL` |
| Persistent disks (`disk:` in `render.yaml`) | No persistent disk: keep state in [managed PostgreSQL](postgres.md) or [object storage](object-storage.md) |
| WebSockets / SSE | `--websocket-enabled` on your app or [managed realtime](realtime.md) (beta) so connections stay open while instances scale to zero |
| Preview environments | [Preview environments](preview-environments.md) |
| Custom domains | [Custom domains](domains.md) |
| Logs | `gregale logs my-api` ([Logs](logs.md)) |
| Rollback | Automatic canary [rollouts](rollouts.md) (1% → 10% → 50% → 100% with automatic 5xx rollback) and `gregale rollback my-api --to v41 --restore-databases` ([Safe releases](releases.md#rollbacks-that-restore-the-database)) |
| Spend limits | Hard monthly spend caps enabled on every paid plan ([Cost controls](cost-controls.md)) |
| Blueprint (`render.yaml`) | `gregale.yaml`, or a Compose file for several services |

## Translate the service settings

A Render web service like this:

```yaml
# render.yaml
services:
  - type: web
    name: my-api
    runtime: python
    buildCommand: pip install -r requirements.txt
    startCommand: gunicorn app:app
    preDeployCommand: python manage.py migrate
    healthCheckPath: /healthz
```

becomes:

```yaml
# gregale.yaml
hosting:
  start: gunicorn app:app --bind 0.0.0.0:$PORT
  health: /healthz
release:
  command: python manage.py migrate
```

Gregale installs dependencies from the manifest itself (and runs a Node
`build` script when there is one), so there is no separate build command;
custom build steps belong in a `Dockerfile`. Name the bind address and port
in the start command: Gregale sets `PORT` for every app.

Check what Gregale detects, from the repository root:

```bash
gregale deploy --dry-run
```

## Copy the configuration and deploy

Put the service's environment variables in a `KEY=VALUE` file, one per line,
with values exactly as they should arrive (no quotes). Then deploy:

```bash
gregale deploy --name my-api --secrets-file render.env
rm render.env
```

The values are sealed before the first deployment starts. If it fails:

```bash
gregale app health my-api --errors
gregale logs my-api
```

## The database

Gregale has no one-step import from Render Postgres. Either keep using it
for now (apps reach outside hosts on ports 80 and 443 only, so on Pro and
Scale open the port with `gregale app egress-ports add my-api 5432`), or create a
managed database and restore a `pg_dump` into it:

```bash
gregale add postgres --app my-api
gregale postgres attach my-api-postgres my-api --access migration --env MIGRATION_DATABASE_URL
```

Restore through [Connect from your laptop](postgres.md#connect-from-your-laptop)
(it needs app tasks on your plan),
and run migrations with `MIGRATION_DATABASE_URL`, the credential that can
change the schema. `DATABASE_URL` goes through a connection pooler in
transaction mode; see [Managed PostgreSQL](postgres.md#connect-an-app).

## Several services

A Blueprint with a web service, private services and background workers
becomes one [project](multi-service-apps.md): describe the services in a Compose
file and deploy it with `gregale deploy`. Services call each other by name,
a service without `ports:` runs all the time as a worker, shared
`envVarGroups` move into project/app environment variables and
[secrets](secrets.md), and persistent `disk:` mounts move to
[managed PostgreSQL](postgres.md) or [object storage](object-storage.md).

## Switch traffic

Add your domain to the Gregale app and verify it as
[Custom domains](domains.md) describes, then move DNS. Suspend the
Render service once the Gregale app has served traffic for a while.
