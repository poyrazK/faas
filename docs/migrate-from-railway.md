# Migrate from Railway

A Railway service usually moves to Gregale without code changes: both build
from source or a Dockerfile, set `PORT`, and pass configuration as
environment variables. Gregale also reads a `railpack.json`:
its `deploy.startCommand` becomes the app's start command, and its
`packages` pin the Node and Python versions. Railway's service settings
(`railway.json`, `railway.toml` or `.railway/railway.ts`) do not carry over;
this page maps them to Gregale's and walks through a move.

## How Railway maps to Gregale

| Railway | Gregale |
| --- | --- |
| Service | App, deployed with `gregale deploy` |
| Replica | Instance: a Firecracker microVM per instance. Idle apps park and wake on the next request. |
| Railpack build | Source builds detect Node, Python and Go ([Deploy a web framework](frameworks.md)); a `railpack.json` start command and package versions are read |
| Dockerfile build | Your `Dockerfile`, as is ([Container compatibility](container-compatibility.md)) |
| Start command | `hosting.start` in `gregale.yaml` |
| Pre-deploy command | A [release command](releases.md#releases-that-change-the-database): `release.command` in `gregale.yaml` or `release:` in a `Procfile` |
| Healthcheck path | `hosting.health` in `gregale.yaml` |
| Serverless (app sleeping) | The default: about a second after its last request an idle instance pauses and stops being billed, and it parks after the app's idle timeout ([Scale to zero](cold-wake.md)) |
| Cron service | A [cron](crons.md) that calls a path on your app, or a scheduled [job](jobs.md) that runs an image to completion |
| Private networking (`api.railway.internal`) | Services in the same [project](multi-service-apps.md) call each other by name: `http://api.internal:10081` |
| Variables and shared variables | [Secrets](secrets.md) (`gregale env set`) and [environment variables](env.md) |
| Railway Postgres | [Managed PostgreSQL](postgres.md), bound to the app as `DATABASE_URL` |
| Redis, MySQL, MongoDB | No managed equivalent: keep them on Railway or another provider |
| Volume | No persistent disk: keep state in [managed PostgreSQL](postgres.md) or [object storage](object-storage.md) |
| PR environments | [Preview environments](preview-environments.md) |
| Custom domains | [Custom domains](domains.md) |
| `railway logs` | `gregale logs my-api` ([Logs](logs.md)) |
| Rollback in the dashboard | `gregale rollback my-api --to v41` ([Safe releases](releases.md)) |

## Translate the service settings

A Railway service whose start command is `gunicorn app:app`, whose pre-deploy
command is `python manage.py migrate` and whose healthcheck path is
`/healthz` becomes:

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
in the start command: Gregale sets `PORT` for every app. When the repository
already has a `railpack.json` with `deploy.startCommand`, or a `Procfile`
with `web:`, you can leave `hosting.start` out.

Check what Gregale detects, from the repository root:

```bash
gregale deploy --dry-run
```

## Copy the variables and deploy

From a directory linked to the Railway service (`railway link`), export its
variables as `KEY=VALUE` lines and drop the ones Railway sets itself:

```bash
railway variables --kv | grep -v '^RAILWAY_' > railway.env
```

Remove the lines you will not use on Gregale (for example `DATABASE_URL` if
you move to managed PostgreSQL). `--secrets-file` takes each value exactly as
written, without quotes; values that span several lines do not fit this
format, so set those with `gregale env set` after the first deploy. Then
deploy:

```bash
gregale deploy --name my-api --secrets-file railway.env
rm railway.env
```

The values are sealed before the first deployment starts. If it fails:

```bash
gregale app health my-api --errors
gregale logs my-api
```

## The database

Gregale has no one-step import from Railway Postgres. To reach it from your
laptop, add Public Access in the database's networking settings on Railway;
that sets `DATABASE_PUBLIC_URL`, and traffic through it is billed as Railway
egress. Then either:

- **Keep Railway Postgres for now.** Put `DATABASE_PUBLIC_URL`'s value in
  `railway.env` as `DATABASE_URL`. Apps reach outside hosts on ports 80 and
  443 only, so on Pro and Scale open the port in that URL with
  `gregale app egress-ports add my-api <port>`; see
  [Networking](networking.md#outbound-connections).
- **Move to managed PostgreSQL.** Create a database and bind it:

  ```bash
  gregale add postgres --app my-api
  gregale postgres attach my-api-postgres my-api --access migration --env MIGRATION_DATABASE_URL
  ```

  Take a `pg_dump` through `DATABASE_PUBLIC_URL` and restore it through
  [Connect from your laptop](postgres.md#connect-from-your-laptop) (it needs
  app tasks on your plan), then point the app's migrations at
  `MIGRATION_DATABASE_URL`, the credential that can change the schema.
  `DATABASE_URL` goes through a connection pooler in transaction mode; see
  [Managed PostgreSQL](postgres.md#connect-an-app) for what that changes.

## Several services

A Railway project with several services becomes one
[project](multi-service-apps.md). Gregale finds the services in a Compose
file, in workspace members, or in folders such as `services/` and `apps/`,
and deploys them together with `gregale deploy`:

- A service that serves requests on Railway's private network is called by
  name on Gregale. Replace a reference variable such as
  `http://${{api.RAILWAY_PRIVATE_DOMAIN}}:8080` with `http://api.internal:10081`.
- A Compose service without `ports:` that no other service calls runs all
  the time as a background worker.
- A Railway cron service runs its start command on a schedule and exits.
  Expose the task as a route your app protects and call it with a
  [cron](crons.md), or push the task's image to a registry and run it as a
  scheduled [job](jobs.md) (`gregale job create --image ... --schedule`).

## Switch traffic

Add your domain to the Gregale app and verify it as
[Custom domains](domains.md) describes, then move DNS. Remove the Railway
service once the Gregale app has served traffic for a while.
