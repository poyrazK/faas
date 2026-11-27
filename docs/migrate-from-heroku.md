# Migrate from Heroku

A Heroku app usually moves to Gregale without code changes: Gregale reads the
same `Procfile`, builds from source without a Dockerfile, and passes
configuration as environment variables. This page maps Heroku's pieces to
Gregale's and walks through a move.

## How Heroku maps to Gregale

| Heroku | Gregale |
| --- | --- |
| App | App, deployed with `gregale deploy` |
| Dyno | Instance: a Firecracker microVM per instance. Idle apps park and wake on the next request. |
| Buildpacks | Source builds detect Node, Python and Go ([Deploy a web framework](frameworks.md)), or use your `Dockerfile` |
| `Procfile` `web:` | Read as is: it is the app's start command |
| `Procfile` `release:` | Read as is: a [release command](releases.md#releases-that-change-the-database) that runs before the new version takes traffic |
| `worker` dyno | A background worker service in a [project](multi-service-apps.md): a service without `ports:` runs all the time |
| Config vars | [Secrets](secrets.md) (`gregale env set`) and [environment variables](env.md) |
| Heroku Postgres | [Managed PostgreSQL](postgres.md), bound to the app as `DATABASE_URL` |
| Heroku Redis and external add-ons | No managed Redis equivalent: keep external add-ons over HTTPS or open ports on Pro/Scale (`gregale app egress-ports add my-api <port>`, [Networking](networking.md#outbound-connections)) |
| Heroku Scheduler | A [cron](crons.md) that calls a path on your app, or a scheduled [job](jobs.md) (`gregale job create --image ... --schedule`) |
| Review apps | [Preview environments](preview-environments.md) |
| Custom domains and ACM | [Custom domains](domains.md) |
| `heroku logs --tail` | `gregale logs my-api` ([Logs](logs.md)) |
| Preboot and `heroku releases:rollback` | Automatic canary [rollouts](rollouts.md) (1% → 10% → 50% → 100% with automatic 5xx rollback) and database-synced rollback (`gregale rollback my-api --to v41 --restore-databases`, [Safe releases](releases.md#rollbacks-that-restore-the-database)) |
| Spend limits / dyno bill surprises | Hard monthly spend caps enabled on every paid plan ([Cost controls](cost-controls.md)) |

## Before you start

- The app must listen on `0.0.0.0` and the `PORT` environment variable, as
  it already does on Heroku.
- `web:` must start a production server (Gunicorn, Uvicorn, `node`), not a
  development one.
- Settings that switch on Heroku's `DYNO` variable stay off on Gregale.
  Heroku's own getting-started apps open `ALLOWED_HOSTS`, the database and
  HTTPS settings only when `DYNO` is set; key them on a variable you set
  with `gregale env set` instead.
- Heroku's buildpacks do work at build time that Gregale does not: Django's
  `collectstatic` (run it when the app starts, into a writable
  `STATIC_ROOT`; see [Deploy a Django app](deploy-django.md#static-files)),
  and a Go binary named after the app (start `./out`; see
  [Deploy a Go app](deploy-go.md)).
- Keep the Heroku app running until the Gregale one serves real traffic.

Check what Gregale detects, from the repository root:

```bash
gregale deploy --dry-run
```

## Copy the configuration

Export the config vars as `KEY=VALUE` lines. `--secrets-file` takes each
value exactly as written, so export JSON and convert it rather than using
`heroku config --shell`, which quotes some values:

```bash
heroku config --json --app my-heroku-app \
  | jq -r 'to_entries[] | "\(.key)=\(.value)"' > heroku.env
```

Remove the lines you will not use on Gregale (for example `DATABASE_URL` if
you move to managed PostgreSQL). Values that span several lines do not fit
this format; set those with `gregale env set` after the first deploy.

## Deploy

```bash
gregale deploy --name my-api --secrets-file heroku.env
rm heroku.env
```

The values are sealed before the first deployment starts. The deploy prints
the app's URL when it is live. If it fails:

```bash
gregale app health my-api --errors
gregale logs my-api
```

## The database

Gregale has no one-step import from Heroku Postgres. Two ways to move:

- **Keep Heroku Postgres for now.** Leave `DATABASE_URL` in `heroku.env`.
  Apps reach outside hosts on ports 80 and 443 only, so on Pro and Scale open
  the database port first with `gregale app egress-ports add my-api 5432`; see
  [Networking](networking.md#outbound-connections).
- **Move to managed PostgreSQL.** Create a database and bind it:

  ```bash
  gregale add postgres --app my-api
  gregale postgres attach my-api-postgres my-api --access migration --env MIGRATION_DATABASE_URL
  ```

  Take a `pg_dump` of your Heroku database (for example, `pg_dump "$(heroku config:get DATABASE_URL --app my-heroku-app)" --no-owner --no-acl -Fc -f heroku.dump`) and restore it
  through [Connect from your laptop](postgres.md#connect-from-your-laptop)
  (it needs app tasks on your plan),
  then point the app's migrations at `MIGRATION_DATABASE_URL`, the
  credential that can change the schema. `DATABASE_URL` goes through a
  connection pooler in transaction mode; see
  [Managed PostgreSQL](postgres.md#connect-an-app) for what that changes.

## Workers and scheduled jobs

- A `worker:` process becomes a background worker service. Describe the web
  and worker processes in a Compose file and deploy it as a
  [project](multi-service-apps.md); a service without `ports:` runs all the time.
- Heroku Scheduler and one-off Rake, `manage.py`, or CLI tasks become HTTP
  [crons](crons.md) (expose each task as a protected HTTP route) or scheduled
  [jobs](jobs.md) (`gregale job create nightly --image registry.example/app:v1 --schedule "0 3 * * *"`).

## Switch traffic

Add your domain to the Gregale app and verify it as
[Custom domains](domains.md) describes, then move DNS. When the
Gregale app has served traffic for a while, scale the Heroku dynos to zero
and remove the Heroku app.
