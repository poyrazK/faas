# Deploy a multi-service app

A repository with several services (a Compose file, a workspace, or folders
such as `services/api` and `services/web`) deploys as one **project**: one
plan, one review, and services that can call each other by name.

Each service counts toward your plan's app limit. Free supports a project
with one service. A project with two or more services requires Hobby or higher
and enough remaining app capacity; projects do not add a separate allowance.

## Deploy the repository

From the repository root:

```sh
gregale deploy
```

When the root is not itself one app, `gregale deploy` plans the whole
repository as a project. It shows every service it will create or update and
asks before creating anything. In CI, pass `--yes` instead of answering the
prompt:

```sh
gregale deploy --yes
```

Every service the deploy builds or redeploys counts once against your
account's hourly deploy limit (`GET /v1/account/rate-limits` shows what is
left). When the deploy needs more than remain, none of its services deploy,
so the project never ends up half on the new release; the plan warns first.
Deploy again after the reset, or deploy fewer services with `--only`. A
GitHub push that does not fit fails every service's check the same way.

`gregale deploy --project` asks for a project deploy explicitly, for example
when the root is also an app. `gregale start` offers **All of them, as one
project** when it finds two or more services. A repository connected to GitHub
deploys the same way without a local checkout:

```sh
gregale deploy --repo octo/shop --ref main --project
```

Add `--only api,web` or `--exclude worker` to deploy part of the project, and
`--diff` to see the plan without changing anything.

Persisted exclusions also apply to previews and match allocated app slugs as well
as service names and roots. If every service requested by `--only` is excluded,
the deployment stops with `project_selection_empty`. Clear or adjust the persisted
exclusions before retrying. A protected environment approval covers the exact
reviewed selection; changing filters requires a fresh scan and approval. Plans
created before selection binding was added must also be scanned again before
approval in a protected environment.

`gregale project get` lists each service with its app, its latest deployment
and the services it declares it calls:

```text
$ gregale project get shop
...
  services:
    NAME    APP          DEPLOYMENT  CALLS
    web     shop-web     live        api, worker
    api     shop-api     live        -
    worker  shop-worker  failed      -
```

The project's page in the dashboard shows the same, plus which services call
each one. Where the [service map](service-map.md) is available to your
account, it also shows the measured calls between the project's services over
the last hour: volume, errors, p95 latency, and how often a call woke a parked
service.

## Deploy to a named environment

For an existing stage:

```sh
gregale deploy --project --env staging
```

Deploying a project to a named stage updates the selected services in that
stage's workload settings. It does not change existing production app settings,
source checkpoints, routes or triggers, and omitting a service leaves its app
and deployments intact. New services reserve app capacity and deploy only in
the selected stage. Shared service renames go through a production project plan.
Each stage deploy creates a deployment for every selected service and pins its stage settings;
deploying that source to production still creates its own deployments.

A service configured only in a named stage is preserved when a production
deploy omits it, and still counts toward the app limit. Selecting it in a
production deploy establishes production membership. Later production plans
can remove that shared app, affecting all its environments; review those
removals before applying. Delete an unused stage-only app explicitly to free
its capacity.

Safe releases and release graphs include the selected environment's services.
A service confined to another stage does not block the release. Promotion
checks both the source and destination membership: a destination service missing
from the source still blocks promotion so it cannot disappear silently.

Production deploys advance existing saved production workload settings in the
same transaction as the app update. New deployments pin those updated settings;
older deployments keep their original settings for recovery.

Source change detection follows the build context. Workspace and convention
builds, and Compose builds using Railpack, include the whole repository, so a
root lockfile or shared package edit redeploys the affected project services.
Compose Dockerfile builds use their declared build context. The first deploy
after this change refreshes the stored source hashes once.

## What becomes a service

Gregale reads, in this order:

- **Compose** (`compose.yaml`, `docker-compose.yml`, plus `compose.override.yaml`
  as `docker compose up` applies it): each service with `build:` or a stateless
  `image:` becomes an app.
- **Workspaces**: members of `package.json` workspaces, `pnpm-workspace.yaml`,
  `go.work`, Cargo, Nx or Turborepo that carry a Dockerfile or language marker.
- **Folders**: each member of `services/`, `apps/`, `packages/` or `cmd/` with a
  Dockerfile or language marker.
- **Go commands**: in a Go module with a `go.mod` at the root and no `main`
  package there, each `cmd/<name>` main package becomes a service named
  `<name>` once there are two or more. Each builds from the module root, so
  shared `internal/` packages are included, and runs only its own binary. The
  plan shows it as `builds=cmd/<name>`. A root `main` package, a single
  command or a root Dockerfile keeps the repository as one app.

Datastore images in Compose (`postgres`, `redis`, `mysql`, `mongo` and similar)
are not run as apps. Only `postgres` has a managed equivalent (below). For the
others the plan says so and lists each service variable that points at them
and will have no value, such as `api.REDIS_URL`; set those to an external
provider before the first request. A GitHub push does not deploy a service
whose variable is still unset, and its check names the variable.

Where [managed PostgreSQL](managed-postgres.md) is available to your account, a
`postgres` service becomes a managed database named `<project>-<service>` in
production (for example `shop-db`), created and bound by the deploy.
Each service whose Compose variable points at it, such as
`DATABASE_URL: postgres://app:secret@db:5432/app`, gets that variable bound;
a service that only lists it in `depends_on` gets `DATABASE_URL`. The database
runs the PostgreSQL major of the service's image (`postgres:17` gets 17), or the
nearest newer one Gregale offers; an untagged image gets the newest. The plan
shows the database and its version under **Managed PostgreSQL (provided by this
deploy)**. A variable
you already set on the app keeps its value, and a later deploy reuses the same
database. Removing the service never deletes the database. In an organization-owned
project the database belongs to the organization, and the deploy needs a role
and key that may create and bind organization databases (`postgres:manage`);
otherwise the plan lists the service with the reason `permission`.

Each named environment gets a separate default database. Its name includes the
project, environment and service with a short hash, and the plan shows the exact
name. These databases count toward the database limit; an exhausted limit leaves
the stage database unprovisioned. Existing scoped variables and bindings retain
their configured database, including bindings created before this isolation rule.
Review and change any older stage binding that intentionally or accidentally
points at production before running migrations there.

Other datastores, and `postgres` where managed PostgreSQL is not available, are
listed under **Managed (not provisioned)** with the variable your services
expect. Point that variable at a database you run elsewhere.

Only a reviewed deploy (`gregale deploy`, the dashboard) creates the managed
database. When a GitHub push adds a `postgres` service, the services that need
its variable and have no value for it are not deployed by that push; their
GitHub check says so. Run a reviewed deploy, or set the variable yourself, and
the next push deploys them.

### Start order and migrations

`depends_on` keeps its Compose meaning where Gregale can honour it:

- `condition: service_healthy` on another service holds this service's new
  release until that service's deployment is live: the one being deployed
  with it, or the one already serving. A failed or rolled-back release of the
  dependency does not block it.
- `condition: service_healthy` on a datastore (`db: postgres:16`) is accepted as
  a plain dependency, because Gregale does not run the datastore as a service.
  The plan says so; have the service retry its connection until the database
  answers, as it does when Compose restarts the database.
- A step the others wait for with `condition: service_completed_successfully`,
  such as a migration, runs as the **release command** of the waiting service
  that shares its build:

  ```yaml
  services:
    migrate:
      build: ./api
      command: npm run migrate
    api:
      build: ./api
      ports: ["3000:3000"]
      depends_on:
        migrate:
          condition: service_completed_successfully
  ```

  `npm run migrate` runs once before each `api` release goes live, in the `api`
  image with its variables and secrets, and `migrate` is not deployed as a
  service. Every deploy of `api` runs it, GitHub pushes included, unless the
  `api` source declares its own `release.command` or Procfile `release:`. Other
  services that waited for `migrate` no longer wait; the plan names them. The
  step must use the same `build:` (context, Dockerfile, args) or `image:` as a
  waiting service that serves requests, must have a `command:` and must not
  publish ports; chained steps and two steps for one service fail the plan.

  The step's environment declarations become defaults for its owner. With
  `x-gregale-env-import: true`, eligible plain values are imported there;
  the owner's explicit declarations and values already configured on the
  app keep precedence. Secret values still require secret configuration.
  A managed database binding uses the variable the step declared, including
  a custom variable such as `DB_CONNECTION`.

  An explicit migration `entrypoint:` is retained in its release command.
  Its supported secret-file mounts are added to the owning service, so they
  are available during the release command and while the service runs. A mount
  that conflicts with the owner's secret source or permissions fails the plan.
  The migration's `user:` and `working_dir:` must match its owner's settings,
  including when one side inherits the image default. The plan rejects a mismatch
  because the release command runs in the owner's runtime.
- A service can also declare its own release command in its own directory:
  `release.command` in its `gregale.yaml`, or `release:` in a `Procfile` there
  (`services/api/gregale.yaml`, `services/api/Procfile`). It runs before each
  release of that service, as on a single-app deploy of the directory, and
  wins over a Compose step. When another service builds from the same
  directory the deploy fails with the reason, because the command would run
  once per service; declare it as a Compose step instead.

Each service keeps its name inside the project. Its app slug is the same name,
or `<project>-<name>` when the name is reserved (`api`, `admin`, …) or taken by
another app, for example `shop-api`.

## Public and internal services

A Compose service that publishes `ports:` gets a public URL. One without
`ports:` is **internal**: other services in the project can call it, and it has
no public URL. So is one whose ports are all bound to a loopback address, such
as `127.0.0.1:8081:8080`, which Docker publishes only on your own machine; the
plan warns, and removing the address publishes it. Other services are public unless
the root [`gregale.yaml` `project:` block](#without-a-compose-file) says `visibility: internal`.
The plan lists every public service under **Public on the internet** and marks
the rest `visibility=internal`. A production deploy also makes an existing
service internal once Compose stops publishing its ports, and the plan says
so: removing `ports:` takes a service off the internet. The reverse is never
automatic: a service that starts publishing ports stays internal, and the plan
says how to publish it (`gregale app <slug> --visibility public`). A deploy to a
named environment changes no visibility, which is app-wide. See
[networking](networking.md) for changing it by hand.

A service listens on the container side of its first `ports:` mapping: with
`ports: ["8080:3000"]`, Gregale sends requests to port 3000 and, unless you set
`PORT` yourself, sets it to 3000, whether the service is built from source or
runs an `image:`. A built
service without `ports:` uses its Dockerfile's single `EXPOSE`, or `PORT`
(8080).

A service without `ports:` serves requests (`class=http` in the plan) when
another service calls it:

- another service's `environment:` addresses it by URL, such as
  `API_URL: http://api:8000`, or
- a service that serves requests lists it in `depends_on`, and it shows that it
  listens: it declares the port with `expose:`, or it runs its own image's
  command (no `command:` override) and its Dockerfile has `EXPOSE`, or, built
  without a Dockerfile, it is an Express, Fastify, NestJS, Hono, FastAPI,
  Flask, Django or Gin app, or, for a prebuilt `image:`, the image's config
  exposes a port. Gregale reads public images' configs; a private image
  counts only with `expose:` or a URL.

Like any service, it then parks when idle and wakes when called. Any other
service without `ports:` is a background worker (`class=worker`): it runs all
the time and a request never wakes it. When a web service lists a worker in
`depends_on`, the plan warns, because Compose also uses `depends_on` for start
order; if the web service does call it, add `expose:` or the URL.

## Call another service

Services in the same project and environment can call each other by name:

```sh
curl http://billing.internal:10081/invoices
```

`http://billing.svc.gregale:10081` is the same service. Calls stay on
Gregale's private network and wake a parked service when needed.

A call stays in its caller's environment: production reaches the production
deployment of `billing`, and a `staging` workload reaches the `staging`
deployment. When `billing` has no deployment in that environment, the call is
refused with 403 rather than sent to production; deploy `billing` to the
environment first. A deploy to the environment that leaves out a dependency it
does not have there says so in its plan. During
development, `gregale dev --all` runs every service in its own developer
environment, and the same names reach those environments instead of
production ([gregale dev](gregale-dev.md#call-the-other-apps)).

A name is called in its DNS form: lower case, with `_` and `.` written as `-`.
A Compose service `billing_api` is `http://billing-api.internal:10081`, and its
dependents get `GREGALE_SERVICE_BILLING_API_URL` pointing there. The plan
warns when a name cannot be called this way, or when two services share one.

Addresses from Docker, such as `http://billing:8080`, do not resolve on Gregale.
A Compose `environment:` value that calls another service this way is imported
as that service's Gregale address on reviewed deploys, without any opt-in
(see [Configuration](#configuration)). In code, use the `.internal` name, or
declare the dependency and read the URL Gregale injects:

```yaml
services:
  web:
    build: ./web
    depends_on: [billing]   # web gets GREGALE_SERVICE_BILLING_URL
```

The project policy is the default for new project services. To allow only the
services a workload declares in `depends_on`, set
`x-gregale-service-policy: declared` on it in Compose, or set it once for the
whole file at the top level. [Networking](networking.md) covers caller
allowlists, per-route scopes, HTTPS between services and timeouts.

### gRPC services

A service that serves gRPC declares it in Compose, so the platform reaches it
over HTTP/2 from the internet and from its siblings:

```yaml
services:
  orders:
    build: ./orders
    x-gregale-app-protocol: grpc   # http1 (default), http2 or grpc
```

Each production deploy sets the app's protocol to the declared value, and a
deploy to a named environment sets it on the services it creates; the protocol
is app-wide, so a stage deploy leaves an existing app's alone. A service that
declares none keeps the protocol set with
`gregale app update <app> --app-protocol`. gRPC needs the Hobby plan or
above, and the plan refuses it on Free. A streaming RPC between services, like
any streamed response, ends after about 15 minutes; clients reconnect and
resume ([long-lived streams](networking.md#long-lived-streams)).

Plain TCP between services (Redis, PostgreSQL and other non-HTTP protocols)
is off unless your operator has enabled it; see
[private TCP](networking.md#private-tcp-between-services).

## Without a Compose file

A repository without Compose declares the same things in the `project:` block
of the `gregale.yaml` at its root:

```yaml
project:
  service_policy: project        # project, declared or account, for every service
  services:
    billing:
      visibility: internal       # no public URL; set when the app is created
      depends_on: [ledger]       # billing gets GREGALE_SERVICE_LEDGER_URL
    admin:
      service_policy: declared   # admin may call only what it declares
```

Service names are the ones the plan shows. The block also applies to Compose
services, but Compose stays the more specific source: a Compose
`x-gregale-service-policy` wins, and `depends_on` lists from both are merged.
A project created before the `project` policy became the default switches by
setting `service_policy: project` here. An unknown name, key or value fails
the plan and names the file. A root `gregale.yaml` that holds only the
`project:` block is not deployed as a service.

To see which file sets a value, run `gregale config explain` with the key,
for example `gregale config explain services.billing.visibility`. It prints
the effective value and every layer that can set it, and marks the one that
wins.

The same settings used to live in `.gregale/project.yaml`. That file still
works but is deprecated: deploys warn about it, and declaring settings in
both places fails the plan. Move it with `gregale project import`, which
appends the block to your `gregale.yaml` without changing the rest of the
file and deletes the old one (`--dry-run` prints the result first).

## Configuration

Compose `environment:` and `env_file` entries name the variables a service
needs. The plan lists every one that will have no value in the target
environment under **Needs a value**; set them before the first request:

```sh
gregale env set --app shop-api LOG_LEVEL=info
gregale env set --app shop-api STRIPE_KEY=sk_live_…
```

or pass a `--secrets-file` to `gregale deploy`. Each key goes only to the
selected services whose Compose `environment:` names it, so one service's
credentials do not reach the others. A key no service names goes to every
selected service that has no value for it yet. A value that has not changed is
not rewritten, and every service's secret limit is checked before the first
write.

A value every service needs, such as a shared API key or log level, can be set
once for the project instead of once per service. Every service of the
environment receives it at its next boot, including services added later, and
a service that sets the same key itself keeps its own value:

```sh
printf 'PAYMENTS_KEY=%s\n' "$PAYMENTS_KEY" | \
  gregale environment secrets set shop production --from-stdin
gregale environment config set shop production --file production.json --yes
```

Deployments in the `default` scope read the `production` environment's values.
See [Project environment secrets](secrets.md#project-environment-secrets) and
[Shared project configuration](env.md#shared-project-configuration).

To copy the plain values from `environment:` instead of typing them again,
opt in at the top of the Compose file:

```yaml
x-gregale-env-import: true
services:
  api:
    build: ./api
    environment:
      LOG_LEVEL: info            # imported
      STRIPE_KEY: sk_test_…      # not imported: looks like a secret
      BILLING_URL: http://billing:8080/v1   # imported as http://billing.svc.gregale:10081/v1
      CACHE_URL: redis://cache:6379         # not imported: Docker address
```

An `http`, `https`, `ws` or `wss` value that calls another service of the
project by its Docker name is imported as that service's Gregale address:
`http://billing.svc.gregale:10081` (or `https://billing.internal` where private
service HTTPS is enabled; elsewhere an `https` value imports as the `http`
address, and the plan says so), keeping the path. The service mesh forwards to the service's own port, so the Docker port
is dropped; a value for a second listener of that service, or for another
protocol, needs its address set by hand.
These service addresses are imported even without `x-gregale-env-import`,
because they are never credentials and the Docker address cannot work here; every other plain value still needs the opt-in.

The plan lists imported names under **Imported from Compose**, names each
rewritten address, and gives the reason beside each value it skips. Gregale
never overwrites a value already set on the app, never imports secret-looking
values, other Docker addresses, `${…}` interpolation or `env_file` values, and
never shows values in the plan.
Imports happen on reviewed deploys (`gregale deploy`, the dashboard,
`--repo … --project`). A GitHub push imports only the sibling service
addresses, which a service added by the push needs to call its siblings, and
leaves every other value to a reviewed deploy. Values in
`compose.override.yaml` are imported too, so keep local-only settings out of an
opted-in project.

## Rename, remove or detach a service

Each deploy compares the repository with the project, and the plan shows every
change before anything is applied:

- **Removing** a service from Compose removes its app when the plan of a
  reviewed deploy (`gregale deploy`, the dashboard) is applied. A service that
  still lists it in `depends_on` fails the plan. A GitHub push never removes a
  service: the app keeps serving its last release, and the push's GitHub check
  says a reviewed deploy removes it. A service hidden by an inactive Compose
  `profiles:` entry counts as removed. A removed app can be restored for 7 days
  with `gregale app restore <slug>`. Until it is restored or purged, the app
  keeps its name: a push that brings the service back does not deploy it, and
  its GitHub check says to restore the app or give the service another app name.
- **Deleting one service** with `gregale app delete` is refused while another
  service of the project calls it (a service binding) or lists it in
  `depends_on`, and the error names them. Remove the service from Compose and
  run a reviewed deploy, which shows its dependents in the same plan.
- **Renaming** a service keeps its app when you say what it was called before:

  ```yaml
  services:
    accounts:
      build: ./accounts
      x-gregale-renamed-from: ledger
  ```

  The plan shows one update, and the app keeps its environment values,
  secrets, domains and URL. The
  service is then called by its new name, so services that call it list
  `accounts` in `depends_on` and read `GREGALE_SERVICE_ACCOUNTS_URL`. Without
  the hint, a rename is a removal and a new service, and the new app starts
  without the old one's values, secrets and domains.
- **`gregale project delete`** deletes the project but not its services: each one
  keeps running as a standalone app, with its domains, environment values and
  crons. Detached apps are no longer one project, so they cannot call each
  other by service name; delete them with `gregale app delete`. While its
  services call each other, `gregale project delete --yes` is refused and names
  the calls that would stop working; `--dry-run` lists them too. Add
  `--detach-services` once they no longer call each other by name.

## Roll back

Each service rolls back on its own, to its previous live deployment:

```sh
gregale rollback shop-api
```

To roll the project's latest deploy back, preview the plan and then run it:

```sh
gregale project rollback shop --dry-run
gregale project rollback shop --yes
```

The services that deploy changed roll back to the release each served before
it, one at a time, callers first: a service rolls back before the services it
depends on, the reverse of a deploy, so a caller is never left calling an API
that has already gone back. Each step waits until the service serves its
previous release. The plan shows each service's revisions (`v4 -> v3`) and
which services it leaves as they are:

- a service the latest deploy did not change (it serves another commit, or a
  release more than 15 minutes older); `--all` rolls those back too;
- a service added by the latest deploy, which has no earlier release;
- a service already rolled back.

The command stops at the first service that fails and names the services
already rolled back and those still on their current release. Fix the cause
and run it again: the services already rolled back are skipped, so a rerun
finishes the rollback instead of undoing it. A rollback across a release
command (a migration) needs `--acknowledge-releases`, as for one app.

A project rollback is a sequence of per-service rollbacks, not one atomic
switch: for a moment, a rolled-back service can receive calls from a service
still on its newer release. Keep service APIs compatible across one release,
or use environment release sets ([preview](env.md)), which switch every
service's route together.

## Pull request previews

A pull request preview of a service under the `project` policy runs every
service of the project that serves calls, built from the pull request's
branch, so its calls reach the same pull request's siblings.
Workers, jobs and scheduled services join only when a service lists them in
`depends_on`, so a preview never consumes production work. Each preview
service counts against the plan's pull request preview allowance.

Under the `declared` policy a preview runs the changed service and the services
it lists in `depends_on`. A call outside that set would reach production, so it
is refused by default (`403 preview_production_dependency_denied`). See
[preview environments](preview-environments.md) and [networking](networking.md)
for letting chosen production services accept preview traffic.

A preview never receives production environment values, because they can hold
credentials. It gets:

- the service URLs Gregale injects;
- the plain values its pull request's Compose file declares, under the same
  `x-gregale-env-import: true` opt-in and rules as a deploy; and
- the values and secrets you set for previews on the production app:

  ```sh
  gregale env set --app shop-web --env pr-preview STRIPE_KEY=sk_test_…
  ```

  To set one for every service of the project at once:

  ```sh
  gregale project preview secret set shop STRIPE_KEY=sk_test_…
  printf 'STRIPE_KEY=%s\n' "$KEY" | gregale project preview secret set shop --from-stdin
  gregale project preview secret unset shop STRIPE_KEY
  ```

  It writes the value to each service's `pr-preview` scope; a service added
  later needs the command again.

`pr-preview` values override the PR's Compose values. See
[preview environments](preview-environments.md#configuration).

## Service settings in gregale.yaml

A service may keep a `gregale.yaml` in its directory. Its settings apply on
every deploy of the service, including project deploys and GitHub pushes:
the `lifecycle` block (`after_restore` and `before_checkpoint` hooks, restart
policy, timeouts, replicas), `scaling` and `retry_policy`. For example, a
service that holds a database pool reconnects after a snapshot restore with:

```yaml
lifecycle:
  after_restore:
    path: /internal/after-restore
    timeout_ms: 500
```

See [cold wake](cold-wake.md#refresh-application-state-after-restore).

## Databases and buckets in gregale.yaml

A Compose `postgres` service already becomes a managed database. To declare
resources by name instead, list them in the `gregale.yaml` at the repository
root, give each entry the service it binds to, and add `create` for any that
do not exist yet:

```yaml
databases:
  - database: chronolake-db
    app: gateway
    create:
      postgres_major: 17
buckets:
  - bucket: segments
    app: store
    create: true
```

The plan lists them before you apply: databases under **Managed PostgreSQL
(provided by this deploy)**, buckets under **Object storage (from
gregale.yaml)**. Apply creates whatever is missing and binds it to its service
after the services exist and before they build, so the first deployment
starts with its keys. An entry without `create` binds an existing resource, and
a missing one stops the plan before anything changes.
Deploys never delete a database or bucket. See
[managed PostgreSQL](postgres.md#connect-an-app) for the database fields;
object-storage bindings inject the bucket's S3 settings under
`GREGALE_S3_<BUCKET>_*` unless the entry sets `prefix`. Binding a bucket
needs a credential with `storage:manage`, and creating a database needs
`postgres:manage`. Organization projects attach buckets with
`gregale add bucket` for now; a project deploy refuses `buckets:` there.

## Debugging a project

- `gregale logs --project shop --follow` streams every service's logs, each
  line prefixed with its service.
- `gregale debug trace <trace-id>` joins one request's hops across services,
  with how each was woken. See [tracing](tracing.md).
- `gregale postgres connect <database>` opens `psql` on your machine through
  a bound service. See [managed PostgreSQL](postgres.md#connect-from-your-laptop).
- `gregale app port-forward <service> 5433:<host>:<port>` reaches anything else
  on a service's network.
- `gregale dev bridge` runs one service on your machine against the rest of
  the project in a remote development environment.

## Cold starts and tracing

Parked services wake on their first request, so a chain of cold services wakes
one after another. Wake-ahead lets an app start restoring the services it
usually calls while it is still waking itself. It is on by default for services
under the `project` policy: the first wake restores the services an app lists in
`depends_on`, and the gateways then learn which siblings it calls after waking.
`gregale app wake-ahead <app> off` turns it off. See
[wake-ahead](service-wake-ahead.md). Traces follow a request across services;
see [tracing](tracing.md), a preview on Hobby and above, and the
[service map](service-map.md), an internal preview available only where your
operator has enabled it.
