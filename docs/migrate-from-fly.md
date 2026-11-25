# Migrate from Fly.io

A Fly.io app usually moves to Gregale without code changes: Gregale runs your
`Dockerfile` as is, sets `PORT`, and passes configuration as environment
variables. `fly.toml` settings do not carry over; this page maps them to
`gregale.yaml` and walks through a move.

## How Fly.io maps to Gregale

| Fly.io | Gregale |
| --- | --- |
| App | App, deployed with `gregale deploy` |
| Machine | Instance: a Firecracker microVM per instance. Idle apps park and wake on the next request. |
| `Dockerfile` | Your `Dockerfile`, as is ([Container compatibility](container-compatibility.md)), or a source build without one ([Deploy a web framework](frameworks.md)) |
| `[http_service]` `internal_port` | `PORT`, which Gregale sets for every app; pin a fixed port with `hosting.port` in `gregale.yaml` |
| `[[http_service.checks]]` `path` | `hosting.health` in `gregale.yaml` |
| `[deploy]` `release_command` | A [release command](releases.md#releases-that-change-the-database): `release.command` in `gregale.yaml` |
| `auto_stop_machines` and `auto_start_machines` | The default: about a second after its last request an idle instance pauses and stops being billed, and it parks after the app's idle timeout ([Scale to zero](cold-wake.md)) |
| `min_machines_running` | `gregale app scale my-api --min 1`, billed like any running instance |
| `[[vm]]` `memory` | `gregale app scale my-api --ram 512`, within your plan's limit ([Plans](plans.md)) |
| `[processes]` | Services in a [project](multi-service-apps.md): a Compose service without `ports:` that no other service calls runs all the time as a worker |
| `[env]` | [Environment variables](env.md) |
| Secrets (`fly secrets set`) | [Secrets](secrets.md) (`gregale env set`) |
| `[mounts]` and volumes | No persistent disk: keep state in [managed PostgreSQL](postgres.md) or [object storage](object-storage.md) |
| `<app>.internal` and `<app>.flycast` | Services in the same [project](multi-service-apps.md) call each other by name: `http://api.internal:10081` |
| Managed Postgres | [Managed PostgreSQL](postgres.md), bound to the app as `DATABASE_URL` |
| Tigris object storage | [Object storage](object-storage.md), S3-compatible, or keep Tigris over HTTPS |
| Scheduled Machines, supercronic, Cron Manager | A [cron](crons.md) that calls a path on your app, or a scheduled [job](jobs.md) that runs an image to completion |
| `fly logs` | `gregale logs my-api` ([Logs](logs.md)) |
| Redeploying an earlier image | `gregale rollback my-api --to v41` ([Safe releases](releases.md)) |

## Translate fly.toml

An app configured like this:

```toml
# fly.toml
app = "my-api"

[deploy]
  release_command = "python manage.py migrate"

[http_service]
  internal_port = 8000
  auto_stop_machines = "stop"
  auto_start_machines = true
  min_machines_running = 0

  [[http_service.checks]]
    method = "GET"
    path = "/healthz"
    interval = "15s"
    timeout = "5s"
```

becomes:

```yaml
# gregale.yaml
hosting:
  health: /healthz
release:
  command: python manage.py migrate
```

Stopping idle instances and starting them on the next request is what
Gregale does by default, so those settings need no translation. If the app
reads `PORT`, the `internal_port` needs none either. If it always listens on
the same port, set that port as `hosting.port` (8000 here); see
[Deployment overrides](deploy-overrides.md). The app must listen on
`0.0.0.0`.

Check what Gregale detects, from the repository root:

```bash
gregale deploy --dry-run
```

## Copy the configuration and deploy

Fly does not show secret values (`fly secrets list` shows only names and
digests), so build a `KEY=VALUE` file from wherever you keep them, one per
line, with values exactly as they should arrive (no quotes). Add the
`[env]` values from `fly.toml`, and leave out the variables Fly sets itself,
such as `FLY_APP_NAME`, `FLY_REGION` and `PRIMARY_REGION`. Then deploy:

```bash
gregale deploy --name my-api --secrets-file fly.env
rm fly.env
```

The values are sealed before the first deployment starts. Values that span
several lines do not fit this format; set those with `gregale env set` after
the first deploy. If the deploy fails:

```bash
gregale app health my-api --errors
gregale logs my-api
```

## The database

Gregale has no one-step import from Fly Managed Postgres. Create a database
and bind it:

```bash
gregale add postgres --app my-api --region <region>
gregale postgres attach my-api-postgres my-api --access migration --env MIGRATION_DATABASE_URL
```

Run `fly mpg proxy`, which forwards a local port to the Fly database, and
take a `pg_dump` through it. Restore the dump through
[Connect from your laptop](postgres.md#connect-from-your-laptop) (it needs
app tasks on your plan), then point the app's migrations at
`MIGRATION_DATABASE_URL`, the credential that can change the schema.
`DATABASE_URL` goes through a connection pooler in transaction mode; see
[Managed PostgreSQL](postgres.md#connect-an-app) for what that changes.

An app that keeps SQLite on a Fly volume needs a new home for that data:
Gregale gives instances no persistent disk, so move it to managed
PostgreSQL or [object storage](object-storage.md).

## Process groups and scheduled tasks

- **Process groups.** Each group in `[processes]` becomes a service in a
  [project](multi-service-apps.md). Describe them in a Compose file that
  builds the same `Dockerfile` with a different `command:` per service, and
  deploy it with `gregale deploy`. The group that serves HTTP publishes
  `ports:`; a worker does not. Services that build from the same directory
  cannot share a `release.command`, so write the `release_command` as a
  Compose step the web service waits for
  ([Start order and migrations](multi-service-apps.md#start-order-and-migrations)).
- **Scheduled tasks.** Fly schedules Machines hourly, daily, weekly or
  monthly; Gregale takes a five-field cron expression. Expose the task as a
  route your app protects and call it with a [cron](crons.md), or push the
  task's image to a registry and run it as a scheduled [job](jobs.md)
  (`gregale job create --image ... --schedule "0 3 * * *"`). A supercronic
  process group becomes one of these too.
- **Calls between apps.** An app that reaches another over `.internal` or
  `.flycast` calls it by name inside the project:
  `http://api.internal:10081`.

## Switch traffic

Add your domain to the Gregale app and verify it as
[Custom domains](domains.md) describes, then move DNS. When the Gregale app
has served traffic for a while, scale the Fly app's Machines to zero and
destroy the app.
