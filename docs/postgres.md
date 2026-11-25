# Managed PostgreSQL

Managed PostgreSQL gives an app its own PostgreSQL database. Gregale creates
the database, delivers its connection URL to your app as a secret, keeps a
point-in-time restore window, and stops the compute while the database is idle.

Every plan includes managed PostgreSQL. To see what your plan and a region
support, run:

```sh
gregale postgres capabilities --region eu-central-1
```

Operators: see the [operator guide](managed-postgres.md).

## Plans

| Plan | Databases | Pooled storage | Compute per month | Included restore history | Restore window | Classes |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| Free | — | — | — | — | — | — |
| Hobby | 1 | 1 GiB | 25 CU-hours | 1 GiB | 7 days | development |
| Pro | 3 | 5 GiB | 75 CU-hours | 5 GiB | 7 days | development, burstable |
| Scale | 10 | 20 GiB | 200 CU-hours | 20 GiB | 7 days | development, burstable, production |

- The Free plan includes no managed PostgreSQL: create, `gregale dev
  --postgres`, a Compose `postgres` service and restores answer
  `403 managed_postgres_not_in_plan` there. Upgrade to Hobby or above for a
  database.
- Storage is pooled across your databases: one database may use the whole
  pool.
- Compute suspends when a database is idle, by default after 2 minutes
  without activity. Every wake bills at least that long, so a shorter time
  costs less and a longer one avoids more cold starts. Choose it per database
  from 1 minute to 1 hour:

  ```sh
  gregale postgres suspend-timeout set orders-db 5m
  gregale postgres suspend-timeout set orders-db default
  ```

  A suspended database takes the new time at once; a running one takes it
  the next time it suspends, without dropping connections. `gregale postgres
  get` shows the current value. Always-on compute is not part of these plans.
- <a id="start-with-your-app"></a>**Start with your app.** A suspended
  database normally starts on your app's first query after idle, so that
  request waits for the app's wake and then the database's (a few hundred
  milliseconds). Turn on `wake-with-app` and the database starts as soon as
  an app bound to it starts waking, so the two overlap:

  ```sh
  gregale postgres wake-with-app set orders-db on
  gregale postgres wake-with-app set orders-db off
  ```

  It is off by default. Every app wake starts the database, including wakes
  that never query it (a health check, a static page, a cron job), and each
  start bills at least one suspend timeout of compute. A database is started
  at most once a minute this way. `gregale postgres get` shows
  `wake_with_app`.
- Restore history (what point-in-time restore keeps) and snapshots are
  included up to the size of the storage pool, averaged over the month.
- Without overage, storage is a limit and nothing past the plan is billed.
  Compute, storage, restore history and egress past the plan are billed only
  when you turn on overage; see [Usage and limits](#usage-and-limits).
- A database runs in a single zone. Gregale makes no high-availability
  promise for it; point-in-time restore and the 7-day undo of a delete are the
  recovery paths.
- A plan downgrade waits until your databases fit the new plan: the refusal
  lists the databases to delete and the ones to move to a class the plan
  includes. A database whose delete is scheduled no longer counts.
- A plan can also drop without that check, for example when a subscription
  ends. Databases the new plan no longer includes are then suspended:
  - your oldest databases are kept, up to the plan's database limit;
  - a newer one shows `plan_excess: database_limit`;
  - one in a class the plan does not include shows
    `plan_excess: service_class`.

  `get`, `list` and the dashboard show it, and you get an email. Its data,
  credentials and app bindings are kept, but it does not start until you
  upgrade, delete a database, or resize it to an included class. The next
  usage sweep, within minutes, resumes it.
  - A database still suspended 30 days later is deleted. The email names the
    date, and a reminder follows a week before it. Its app bindings are
    removed, and `gregale postgres recover` brings it back for 7 days after
    that.
- A subscription that ends drops the account to Free, which includes no
  databases, so every database it holds is suspended this way.

## Create a database

```sh
gregale postgres create orders --region eu-central-1
gregale postgres get orders
```

The database starts in `provisioning` and moves to `ready`. Options:

- `--class development|burstable|production`: the compute class your plan
  allows. The default is `development`.
- `--postgres-major N`: the PostgreSQL major version, 15 to 18. The default is
  16. PostgreSQL 14 reached its end of life in November 2026 and is not
  supported.
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

To connect an app to Postgres, attach the database to it. Gregale delivers
the connection URL as an app secret:

```sh
gregale postgres attach orders my-api --env DATABASE_URL
```

- `--scope` picks the environment. It defaults to the linked project's
  environment.
- The app receives the secret once the binding is `ready`. Each binding has
  its own login, so detaching one app never affects another.
- A bound app can reach its database on every plan. Other destinations on port
  5432 stay closed unless the app declares that egress port (Pro and Scale).

> **Schema changes need a second binding.** The default `read_write` access
> can read and write rows in `public` but cannot `CREATE`, `ALTER` or `DROP`
> anything, so a migration run at app startup fails with `permission denied`.
> Attach the database again with `--access migration --env MIGRATION_DATABASE_URL`
> and run your migration tool as a release task pointed at that URL. Serving
> instances never receive it. The access levels are listed under
> [Connect from your laptop](#connect-from-your-laptop).

### Declare the database in gregale.yaml

Instead of `gregale postgres attach`, you can declare the database in
`gregale.yaml`, and `gregale deploy` binds it before it starts the new
compute:

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

Add `create` to have the deploy create the database when none with that name
exists, so a new project needs no `gregale postgres create` first:

```yaml
databases:
  - database: chronolake-db
    app: gateway
    create:                # or `create: true` for the defaults
      postgres_major: 17
      class: development   # development (default), burstable or production
      scale_to_zero: true  # default
      region: us-east-1    # defaults to your default region
  - database: chronolake-db
    app: gateway
    env: MIGRATION_DATABASE_URL
    access: migration
```

- `create` applies only when the database is missing: an existing database
  keeps its settings, and the deploy never changes or deletes one.
- Declare `create` once per database; other entries for the same name bind it.
- A project deploy lists the database under **Managed PostgreSQL (provided by
  this deploy)** before you apply. The database counts against your plan's
  limit, and the plan stops before creating anything when it would not fit.
- `gregale deploy` of a single app waits up to three minutes for the new
  database. A project deploy reserves the bindings right away and delivers each
  key once the database is ready, usually before the first build ends.
- Creating a database needs a credential with `postgres:manage`. Deploy
  tokens and `deploy:write` keys can bind existing databases but not create
  them.

## Connect from your laptop

`gregale postgres connect` opens `psql` on your machine against a managed
database, through an app that is bound to it:

```sh
gregale postgres connect orders
gregale postgres connect orders --access read_only -- -c 'select count(*) from segments'
gregale postgres connect orders --proxy --port 5433   # for a GUI client
```

It starts a task VM of a bound app to read that binding and relays a local
port to the database through
[`gregale app port-forward`](apps.md). TLS still verifies the database's own
host name, and the password reaches `psql` in its environment, so it is never
printed or put on the command line. With `--proxy` the tunnel stays open for
another client; take the password from the app's binding.

- It picks a `ready` binding, preferring `read_write`, then `migration`, then
  `read_only`. Choose one with `--app`, `--env` or `--access`.
- It needs [app tasks](apps.md) on your plan and `psql` on your machine (or
  `--proxy`). The app's image needs `printenv`, which Debian, Ubuntu and
  Alpine images include.

Choose the credential with `--access`:

| Access | Use it for |
| --- | --- |
| `read_write` (default) | The app's queries: SELECT, INSERT, UPDATE and DELETE in the `public` schema, with row-level security enforced. |
| `read_only` | Read-only queries, for example `--env READ_DATABASE_URL`. |
| `migration` | Schema changes. Only the release task receives it, never the serving instances. |
| `data_api` | The [Data API](data-api.md). |
| `session` | The same privileges as `read_write` over a direct connection, for serving code that needs session state. |

`read_write` and `data_api` URLs, and `read_only` ones unless they reach a
replica, go through a connection pooler in transaction mode. Session state does not carry from one transaction to the
next, so these don't work through it:
- `SET` outside a transaction (use `SET LOCAL` inside one);
- `LISTEN` and `NOTIFY`;
- session-level advisory locks (use transaction-level ones);
- SQL-level `PREPARE` (driver prepared statements work);
- temporary tables kept across transactions.

A `migration` binding connects directly and supports all of them, but only the
release task receives it. When the serving app itself needs one of them, bind
a `session` credential instead:

```sh
gregale postgres attach orders my-api --access session --env SESSION_DATABASE_URL
```

Each serving instance then holds its own server connections, so keep its pool
small: a `session` login allows at most 42 connections across every instance
of the app (2 per instance at Scale concurrency), and further connections are
refused with "too many connections for role". Keep ordinary query traffic on a
pooled `read_write` binding. `session` is offered only where the database
backend has qualified it (`gregale postgres capabilities` lists it).

An app holds at most 8 managed PostgreSQL bindings on Free, 25 on Hobby, 50 on
Pro and 100 on Scale. A binding's environment key must start with a letter and
may not be `PORT` or start with `GREGALE_` or `FAAS_`, which the platform sets.

Run migrations as a release task, with a Procfile `release: npm run migrate` or
`release.command` in `gregale.yaml`, and point the migration tool at a
`migration` binding:

```sh
gregale postgres attach orders my-api --access migration --env MIGRATION_DATABASE_URL
```

A failed release command leaves the previous deployment serving. A rollback
never undoes a migration; see
[Releases that change the database](releases.md#releases-that-change-the-database).

To rotate a credential, run `gregale postgres bindings list orders`, then
`gregale postgres bindings rotate BINDING_ID --wait`. Rotation returns once
the new credential is active. `--wait` polls until `rotation_pending` clears,
for up to five minutes (`--wait-timeout`).

Rotation recovery queues the runtime refresh even if the original request
fails or the worker restarts. The previous serving credential stays valid
until that refresh is confirmed; retrying rotation reuses its generation.

The `rest-api-postgres` and `rest-api-postgres-python` starters
(`gregale init --template rest-api-postgres`) already use this layout. They
adopt rotated credentials and reconnect after a restore. See the
[pool runtime guide](postgres-pool-runtime.md).

## Run SQL from the CLI

`gregale postgres query` (alias `postgres sql`) runs a query against a managed
database, through the same bound-app tunnel as above, and prints the rows. It needs no `psql`, so scripts and coding agents
can use it:

```sh
gregale postgres sql orders "select status, count(*) from orders group by 1"
gregale postgres sql orders --file audit.sql --json
gregale postgres sql orders --write "update orders set status = 'void' where id = 42"
```

- It is read-only unless you pass `--write`. A read runs as the only statement
  of a `READ ONLY` transaction that is always rolled back, on a `read_only`
  binding when the database has one. A second statement, or one that writes,
  fails with a hint. Without a `read_only` binding it uses the app's
  credential, so functions with side effects that a read-only transaction
  still allows (`pg_terminate_backend`, advisory locks) run with that role's
  rights; attach a `read_only` binding for a hard boundary.
- `--write` runs the text as a script on a `read_write`, `migration` or
  `session` binding and commits it. To try a destructive change first, run it
  on a [branch](#branches): `gregale postgres branch orders pr-42 --ttl 2h`,
  attach the branch to an app, then `gregale postgres sql pr-42 --write …`.
- `--max-rows` (default 500) caps the rows kept per result, and `--timeout`
  (default 30 s) bounds each run: the statement timeout for a read, a cancel
  of the whole script for `--write`.
- With `--json`, numbers, booleans and `json`/`jsonb` stay typed, `NULL` is
  `null`, and every other value is its PostgreSQL text form. Each result also
  lists its column names and types and the command tag (`SELECT 1204`, which
  counts every row, kept or not).

## Restore to a point in time

A restore creates a new database from any moment inside the restore window. The
source database and its apps are not changed.

```sh
gregale postgres restore orders --name orders-before-import --point-in-time 2026-10-30T09:00:00Z
gregale postgres attach orders-before-import my-api --env RESTORED_DATABASE_URL
```

`gregale postgres get` shows `restore_range`: the earliest and latest point a
restore can target now. The earliest is the later of the database's creation
and the start of its restore window.

Attach the restored database under its own environment key when you want an
app to read it. A source you delete while a database restored from it exists
is kept until that copy is gone; see
[Delete, recover and purge](#delete-recover-and-purge).

The restored database counts toward your plan's database limit. When the
account is at its limit, one restore may still go one database past it, on
every plan:

- The copy shows `restore_slot_until`, 7 days out, in `get`, `list` and the
  API.
- If you purge another database or upgrade before then, the copy becomes an
  ordinary database.
- Otherwise its delete is scheduled at `restore_slot_until`, with the usual
  7-day undo. Its bindings are removed first. The source is never touched.
- The account that ran the restore gets an email 48 hours before
  `restore_slot_until` while there is still no room, and another when the
  delete is scheduled. Recording the expiry email and scheduling deletion
  happen together, so a worker restart cannot lose the notice.
- A second restore past the limit while a copy holds the slot returns
  `403 managed_postgres_restore_slot_in_use`. Purge the copy to restore again.
- A copy can replace its source on a one-database plan: point your app at
  the copy, then delete the source. A normal delete is enough; you do not
  have to purge. Once the source's delete is scheduled, the copy becomes an
  ordinary database and keeps its bindings past `restore_slot_until`. The
  source keeps its 7-day undo, but recovering it then needs room under your
  plan's limit (delete or purge the copy, or upgrade). A purged source is
  kept, with its name and storage, until the copy is deleted.
- A [rollback that restores the database](releases.md#rollbacks-that-restore-the-database)
  never uses the slot: its copy becomes the app's database, so the account
  needs room for it inside the limit.

### Restore and switch an app

`--switch` restores the database and moves an app to the copy in one step,
without changing the app's code. Use it when bad data, not bad code, is the
problem: a mistaken `DELETE`, a broken import, a job that ran twice.

```sh
gregale postgres restore orders --name orders-0900 --point-in-time 2026-10-30T09:00:00Z --switch my-api
```

```text
orders is restored to 2026-10-30T09:00:00Z as a new database, orders-0900, and my-api moves to it; its code does not change.
Writes to orders after that moment stay only in orders, which keeps every write and is no longer bound to my-api.
my-api stops serving while it switches (usually under a minute, then a cold start).
Type "my-api" to confirm: my-api
restoring orders to 2026-10-30T09:00:00Z as orders-0900
staging and verifying credentials for orders-0900
fencing my-api; it stops serving until the switch ends
draining my-api: 2 retired, 1 parked, 0 pending
switching my-api to orders-0900
resuming my-api on v42
✓ Switched my-api to orders-0900. orders keeps every write and is no longer bound to my-api; delete it when you no longer need it.
```

It runs the same steps as a
[rollback that restores the database](releases.md#rollbacks-that-restore-the-database),
except that the release stays the same: restore, stage and verify credentials,
fence and drain the app, move every binding the app has to the database in
the scope (`--env`, default `default`) in one transaction, then resume. Until
the bindings move, a failure or `gregale release rollback status my-api <id>
--cancel` unwinds it, and the app keeps serving on `orders`.

- **Data:** the copy holds exactly what `orders` held at the restore point.
  Writes after it are not copied; `orders` keeps them unchanged, so you can
  compare or copy rows by hand. Nothing is lost by switching.
- **Downtime:** the app does not serve from the fence until the first request
  after the switch, usually under a minute plus a cold start. Instances and
  tasks must settle within 2 minutes of the fence and the drain must finish
  within 10, otherwise the switch fails and the app resumes on `orders`.
- **Room:** the copy becomes the app's database, so it must fit inside your
  plan's database limit; it cannot use the restore slot.
- **One app per switch.** Other apps bound to `orders` stay on it.
- `--yes` skips the prompt (scripts and `--json` need it); `--no-wait`
  returns once the switch starts. `gregale rollback switches my-api` lists
  switches. The API is `POST /v1/apps/{slug}/database-switches` with
  `database_id`, `name`, `point_in_time` and an optional `scope`; it needs the
  `postgres:manage` scope and a verified email.

### Recovery objectives

What a recovery gives back, and how long it takes:

| Situation | Recovery | Data | Time to serve again |
| --- | --- | --- | --- |
| Bad data or a mistaken write | `restore --switch` | everything up to the point you pick | minutes: the restore, then under a minute of downtime and a cold start |
| A bad migration | `gregale rollback --restore-databases` | everything up to the start of the release | the same |
| A database deleted by mistake | `gregale postgres recover`, within 7 days | everything | minutes |
| A provider zone outage | none from Gregale | committed writes are kept by the provider | until the provider recovers; no high-availability promise |

The restore point can be any moment inside the restore window (7 days). The
switch's own steps are bounded: the restore 1 hour, settling 2 minutes,
draining 10 minutes; a step that runs over fails the switch and the app keeps
serving.

## Branches

A branch is a restore from the database's latest point: a new database that
shares its source's storage until it is written to. Use one to test a
migration or a destructive job against real data, then throw it away.

```sh
gregale postgres branch orders pr-42 --ttl 2h
gregale postgres attach pr-42 my-api-pr-42 --env DATABASE_URL
# run migrations and tests against pr-42
gregale postgres branch delete orders pr-42 --yes
```

- `--from live` (the default) takes the latest point, a couple of seconds
  before the request. `--from 2026-11-06T09:00:00Z` branches from any time in
  the source's `restore_range`.
- `--ttl` (5m to 30d, for example `90m`, `2h` or `7d`) purges the branch that
  long after it is created. `get` and `list` show it as `expires_at`. At that
  time its bindings are removed and it is purged: there is no undo window. A
  branch that has branches of its own is kept until they are gone.
- `gregale postgres branch list orders` lists the branches of `orders`.
  `branch delete` purges a branch at once, and refuses a database that is not
  a branch of the one you name.
- The source and its apps are never changed. A source you delete while it has
  branches is kept until they are gone.
- A branch's name is its database name, so it is unique in your account like
  any database name. With several databases, include the source in it, for
  example `orders-pr-42`.
- A branch with a TTL of 7 days or less does not count toward your plan's
  database limit. Each plan includes short-lived branches at once: Hobby 2,
  Pro 5, Scale 20. Past that, branch creation is refused with
  `managed_postgres_branch_limit`; delete a branch or wait for one to expire.
  The branch's compute and the data written to it count toward your plan's
  usage like any database.
- A branch with a longer TTL, or none, is an ordinary restore: it counts
  toward your plan's database limit and may take the one restore slot past
  it (see [Restore to a point in time](#restore-to-a-point-in-time)).
- In the API, a restore without `point_in_time` is a branch from the latest
  point, and `ttl_seconds` sets its expiry.

## Change the compute class

Resizing is in beta.

```sh
REQUEST_ID=$(uuidgen | tr A-Z a-z)
gregale postgres resize orders --class burstable --request-id "$REQUEST_ID"
gregale postgres resize status orders "$REQUEST_ID"
```

- Keep the request ID, a lowercase UUID: repeating it returns the same
  resize's progress.
- A confirmed rejection reports `failed` and `last_error_code`, keeps the
  original configuration and permits deletion or a corrected request with a
  new ID. An uncertain outcome stays `pending` until it can be confirmed.
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
  upgrade, unless compute overage is on. A suspended database keeps its data.
- Compute overage (`gregale postgres overage on|off`) keeps the databases
  running past the allowance, up to twice it, and bills the excess at $0.2331
  per CU-hour. It is off by default on Hobby and Pro and on by default on
  Scale; turning it off on Scale is kept.
- Storage: at the pooled storage limit, writes that would grow a database
  fail. Reads, deletes and `VACUUM` keep working, and nothing is suspended or
  deleted. With overage on, the limit is twice the pool, and storage past the
  pool, averaged over the month, is billed at $0.50 per GiB-month.
- Restore history and snapshots: `usage` shows `history_byte_seconds` (what
  point-in-time restore keeps), `snapshot_byte_seconds` and
  `included_history_bytes`, which equals your storage pool. Restore history
  uses the included amount first. It grows with how much your databases write,
  not with their size: rewriting 1 GiB a day keeps about 7 GiB with a 7-day
  restore window. With overage on, what passes the included amount, averaged
  over the month, is billed at $0.21 per GB-month for restore history and
  $0.0945 per GB-month for snapshots (10^9 bytes). A month that billed it
  keeps the charge if you turn overage off.
- With overage off, restore history is bounded instead of billed. Once this
  month's restore history and snapshots pass twice the included amount,
  `usage` shows `history_state: limited` and your databases keep only the
  last 24 hours of history until the 1st of next month, or until you turn
  overage on. Restoring to a point older than 24 hours is refused while
  limited, and history dropped while limited does not come back. Writes,
  reads and data are not affected.
- Egress: each database has its own 500 GB (10^9 bytes) monthly egress
  allowance, separate from your apps' plan allowance. `usage` shows
  `egress_allowance_bytes` (per database) and what all the databases sent
  (`egress_bytes`). `egress_state` follows the database that sent the most:
  `warning` at 80% of its limit and `held` once it reaches it. That database
  then refuses connections until the 1st of next month; your other databases
  and apps keep running, and the account is emailed once that month. Data a
  database sends your app counts as database egress; private network
  transfer (Private Link) does not. A copy of a database (a restore, a branch
  or a PR preview copy) shares its source's 500 GB: the source and its copies
  are held once together they sent it.
- With egress overage on (`gregale billing egress-overage`), each database's
  egress past its own 500 GB (shared with its copies) is billed at $0.105 per
  GB. `egress_state` is then `overage`, and a database is held only at twice
  its allowance.
- Database overage counts toward your [spend cap](billing.md#spend-cap) and
  is billed only up to it; at the cap your databases are held with
  `hold: compute_ceiling`.

When the platform stops a database that is still `ready`, `get`, `list` and
the API say why in `hold`: `compute_ceiling`, `egress`, `inactive` (the
account is suspended), `plan_excess` or `preview` (a PR preview database once
the account used 80% of its compute). `usage` shows `hold` when every database
of the account is held. A held database keeps its data.

## Delete, recover and purge

```sh
gregale postgres delete orders
gregale postgres recover orders
gregale postgres delete orders --purge
```

`delete` does not delete a ready database at once.
- The database moves to `deleting`. Its compute stops and it refuses
  connections.
- Its `restorable_until`, 7 days out, shows in `get`, `list` and the API.
- Until then, `recover` (API: `POST /v1/postgres/databases/{id}/undelete`)
  returns it to `ready` with its data and restore history.
- After that, it is deleted for good.
- A database that never became ready is deleted at once.

If a delete request fails while the provider is confirming that compute has
stopped, check the database with `gregale postgres get`. Its delete may already
be scheduled. Retrying delete finishes that step without extending the 7 days;
`recover` can still undo it.

`--purge` (API: `DELETE /v1/postgres/databases/{id}?purge=true`) deletes the
database and its restore history at once, even during the 7 days. It cannot be
undone through Gregale. The provider keeps the deleted data for up to 7 more
days before it erases it.

Until it is purged, a deleted database:
- still counts toward your plan's database limit;
- keeps its name, so creating another database with that name returns 409;
- uses nothing from your compute allowance or storage pool, unless it has a
  restored copy (below).

A database you restored a copy from, for example the original database after
a rollback that restored it, can be deleted while the copy lives. The copy is
stored inside the original, so:
- the 7 days to recover it run as usual, and its usage keeps counting, because
  it includes the copy's;
- after that, or at once with `--purge`, it stays in `deleting`, with
  `last_error_code` `restore_copies_remain`. It no longer counts toward your
  database limit, and can no longer be recovered;
- it is deleted for good once you delete its copies. Until then its name stays
  taken, and the storage it holds stays in your storage pool.

Undelete applies the same plan check as create. A database your current plan
no longer allows, for example after a downgrade, cannot be recovered.

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
the same operations. Company databases, in beta, use
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
