# Managed PostgreSQL

Managed PostgreSQL gives an app its own PostgreSQL database. Gregale creates
the database, delivers its connection URL to your app as a secret, keeps a
point-in-time restore window, and stops the compute while the database is idle.

Managed PostgreSQL is a preview, enabled per account. To see whether your
account and a region can create databases, and what they support, run:

```sh
gregale postgres capabilities --region eu-central-1
```

Operators: see the [operator guide](managed-postgres.md).

## Plans

| Plan | Databases | Pooled storage | Compute per month | Restore window | Classes |
| --- | ---: | ---: | ---: | ---: | --- |
| Free | 1 | 0.5 GiB | 5 CU-hours | 7 days | development |
| Hobby | 1 | 1 GiB | 25 CU-hours | 7 days | development |
| Pro | 3 | 5 GiB | 75 CU-hours | 7 days | development, burstable |
| Scale | 10 | 20 GiB | 200 CU-hours | 7 days | development, burstable, production |

- Storage is pooled across your databases: one database may use the whole
  pool.
- Compute suspends when a database is idle. Always-on compute is not part of
  these plans.
- Storage and compute are limits. Nothing past them is billed while managed
  PostgreSQL has no invoicing.

## Create a database

```sh
gregale postgres create orders --region eu-central-1
gregale postgres get orders
```

The database starts in `provisioning` and moves to `ready`. Options:

- `--class development|burstable|production`: the compute class your plan
  allows. The default is `development`.
- `--postgres-major N`: the PostgreSQL major version.
- `--storage-bytes N` and `--restore-window-seconds N`: default to your plan's
  allowance.

To create a database and attach it to an app in one step:

```sh
gregale add postgres --app my-api --region eu-central-1
gregale add postgres --database orders --app my-api --env production
```

`add postgres` reuses a matching database when there is one. Otherwise it
creates one named `<app>-postgres`, waits until it is ready, and binds it to
the app as `DATABASE_URL`. `--database` attaches an existing database by name
or ID. `--region` is needed only when a database is created.

## Connect an app

Attach a database to an app, and Gregale delivers its connection URL as an app
secret:

```sh
gregale postgres attach orders my-api --env DATABASE_URL
```

- `--scope` picks the environment. It defaults to the linked project's
  environment.
- The app receives the secret once the binding is `ready`. Each binding has
  its own login, so detaching one app never affects another.
- A bound app can reach its database on every plan. Other destinations on port
  5432 stay closed unless the app declares that egress port (Pro and Scale).

Or declare the database in `gregale.yaml`, and `gregale deploy` binds it
before it starts the new compute:

```yaml
databases:
  - database: orders       # name or ID
    scope: production      # defaults to default
    env: DATABASE_URL      # defaults to DATABASE_URL
    access: read_write
```

- A binding that is still provisioning blocks the deploy with an error you can
  retry.
- In a project with several apps, give each entry an `app`.
- All of one deployment's database entries use the same scope.

Choose the credential with `--access`:

| Access | Use it for |
| --- | --- |
| `read_write` (default) | The app's queries: SELECT, INSERT, UPDATE and DELETE in the `public` schema, with row-level security enforced. |
| `read_only` | Read-only queries, for example `--env READ_DATABASE_URL`. |
| `migration` | Schema changes. Only the release task receives it, never the serving instances. |
| `data_api` | The [Data API](data-api.md). |

`read_write` and `data_api` URLs, and `read_only` ones unless they reach a
replica, go through a connection pooler in transaction mode. Session state does not carry from one transaction to the
next, so these don't work through it:
- `SET` outside a transaction (use `SET LOCAL` inside one);
- `LISTEN` and `NOTIFY`;
- session-level advisory locks (use transaction-level ones);
- SQL-level `PREPARE` (driver prepared statements work);
- temporary tables kept across transactions.

A `migration` binding connects directly and supports all of them.

Run migrations as a release task, with a Procfile `release: npm run migrate` or
`release.command` in `gregale.yaml`, and point the migration tool at a
`migration` binding:

```sh
gregale postgres attach orders my-api --access migration --env MIGRATION_DATABASE_URL
```

A failed release command leaves the previous deployment serving. A rollback
never undoes a migration; see
[Releases that change the database](deploys.md#releases-that-change-the-database).

To rotate a credential, run `gregale postgres bindings list orders`, then
`gregale postgres bindings rotate BINDING_ID --wait`. Rotation returns once
the new credential is active. `--wait` polls until `rotation_pending` clears,
for up to five minutes (`--wait-timeout`).

The `rest-api-postgres` and `rest-api-postgres-python` starters
(`gregale init --template rest-api-postgres`) already use this layout. They
adopt rotated credentials and reconnect after a restore. See the
[pool runtime guide](postgres-pool-runtime.md).

## Restore to a point in time

A restore creates a new database from any moment inside the restore window. The
source database and its apps are not changed.

```sh
gregale postgres restore orders --name orders-before-import --point-in-time 2026-10-30T09:00:00Z
gregale postgres attach orders-before-import my-api --env RESTORED_DATABASE_URL
```

Attach the restored database under its own environment key when you want an
app to read it. A source cannot
be deleted while a database restored from it exists.

## Change the compute class

```sh
REQUEST_ID=$(uuidgen | tr A-Z a-z)
gregale postgres resize orders --class burstable --request-id "$REQUEST_ID"
gregale postgres resize-status orders "$REQUEST_ID"
```

- Keep the request ID, a lowercase UUID: repeating it returns the same
  resize's progress.
- Clients may be disconnected during the change. They reconnect with the same
  credentials.
- Region, version, storage and the restore window stay as they are.

## Usage and limits

```sh
gregale postgres usage
```

`usage` shows this month's compute, storage, restore history and egress, and
how many databases you have against your plan's limit. It can trail the
databases by up to two hours.
- Compute: `compute_cap_state` is `warning` at 80% of the month's allowance. At the
  allowance the databases are suspended until the 1st of next month or an
  upgrade. A suspended database keeps its data.
- Storage: at the pooled storage limit, writes that would grow a database
  fail. Reads, deletes and `VACUUM` keep working, and nothing is suspended or
  deleted.

## Delete, undelete and purge

```sh
gregale postgres delete orders
gregale postgres undelete orders
gregale postgres delete orders --purge
```

`delete` does not delete a ready database at once.
- The database moves to `deleting`. Its compute stops and it refuses
  connections.
- Its `restorable_until`, 7 days out, shows in `get`, `list` and the API.
- Until then, `undelete` (API: `POST /v1/postgres/databases/{id}/undelete`)
  returns it to `ready` with its data and restore history.
- After that, it is deleted for good.
- A database that never became ready is deleted at once.

`--purge` (API: `DELETE /v1/postgres/databases/{id}?purge=true`) deletes the
database and its restore history at once, even during the 7 days. It cannot be
undone through Gregale. The provider keeps the deleted data for up to 7 more
days before it erases it.

Until it is purged, a deleted database:
- still counts toward your plan's database limit;
- keeps its name, so creating another database with that name returns 409;
- uses nothing from your compute allowance or storage pool.

Undelete applies the same plan check as create. A database your current plan
no longer allows, for example after a downgrade, cannot be undeleted.

`delete` asks you to type the database's name. Scripts pass `--yes` (or
`--quiet`); JSON and `--non-interactive` mode require it. A database with an
attached app cannot be deleted. Detach the app first with
`gregale postgres bindings delete BINDING_ID`.

## Dashboard

The signed-in dashboard's `/dashboard/postgres` page shows your databases,
their bindings and any scheduled delete. It is read-only: create, restore,
delete and bind with the CLI or the API.

## API and SDKs

Every command uses the public API under `/v1/postgres/*`, and the SDKs cover
the same operations. Company databases use
`/v1/orgs/{organization}/postgres/databases`.
- API keys need `postgres:read` to read databases and usage.
- They need `postgres:manage` to create, restore, resize, delete, undelete or
  bind.
- Responses never contain passwords, connection URLs or provider IDs.
  Credentials reach your app only as app secrets.
- Every CLI command takes `--json` and prints the API's response.

## Deleting your account

An account with databases cannot be deleted. Purge each database first:

```sh
gregale postgres list
gregale postgres delete orders --purge
```

A database whose delete is only scheduled still blocks account deletion until
its 7 days end.
