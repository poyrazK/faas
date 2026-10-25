# Deploy a multi-service app

A repository with several services (a Compose file, a workspace, or folders
such as `services/api` and `services/web`) deploys as one **project**: one
plan, one review, and services that can call each other by name.

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

`gregale deploy --project` asks for a project deploy explicitly, for example
when the root is also an app. `gregale start` offers **All of them, as one
project** when it finds two or more services. A repository connected to GitHub
deploys the same way without a local checkout:

```sh
gregale deploy --repo octo/shop --ref main --project
```

Add `--only api,web` or `--exclude worker` to deploy part of the project, and
`--diff` to see the plan without changing anything.

## What becomes a service

Gregale reads, in this order:

- **Compose** (`compose.yaml`, `docker-compose.yml`, plus `compose.override.yaml`
  as `docker compose up` applies it): each service with `build:` or a stateless
  `image:` becomes an app.
- **Workspaces**: members of `package.json` workspaces, `pnpm-workspace.yaml`,
  `go.work`, Cargo, Nx or Turborepo that carry a Dockerfile or language marker.
- **Folders**: each member of `services/`, `apps/`, `packages/` or `cmd/` with a
  Dockerfile or language marker.

Datastore images in Compose (`postgres`, `redis`, `mysql`, `mongo` and similar)
are not run as apps.

Where [managed PostgreSQL](managed-postgres.md) is available to your account, a
`postgres` service becomes a managed database named `<project>-<service>`
(for example `shop-db`), created and bound by the deploy
([ADR-20261025111824](adr/20261025111824-compose-postgres-as-managed-database.md)).
Each service whose Compose variable points at it, such as
`DATABASE_URL: postgres://app:secret@db:5432/app`, gets that variable bound;
a service that only lists it in `depends_on` gets `DATABASE_URL`. The database
runs the PostgreSQL major of the service's image (`postgres:17` gets 17), or the
nearest newer one Gregale offers; an untagged image gets the newest. The plan
shows the database and its version under **Managed PostgreSQL (provided by this
deploy)**. A variable
you already set on the app keeps its value, and a later deploy reuses the same
database. Removing the service never deletes the database.

Other datastores, and `postgres` where managed PostgreSQL is not available, are
listed under **Managed (not provisioned)** with the variable your services
expect. Point that variable at a database you run elsewhere.

Each service keeps its name inside the project. Its app slug is the same name,
or `<project>-<name>` when the name is reserved (`api`, `admin`, …) or taken by
another app, for example `shop-api`.

## Public and internal services

A Compose service that publishes `ports:` gets a public URL. One without
`ports:` is **internal**: other services in the project can call it, and it has
no public URL. Other services are public unless
[`.gregale/project.yaml`](#without-a-compose-file) says `visibility: internal`.
The plan lists every public service under **Public on the internet** and marks
the rest `visibility=internal`. Visibility is set when the app is created; see
[networking](networking.md) for changing it later.

## Call another service

Services in the same project and environment can call each other by name:

```sh
curl http://billing.internal:10081/invoices
```

`http://billing.svc.gregale:10081` is the same service. Calls stay on
Gregale's private network and wake a parked service when needed.

Addresses from Docker, such as `http://billing:8080`, do not resolve on Gregale.
Use the `.internal` name instead, or declare the dependency and read the URL
Gregale injects:

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

## Without a Compose file

A repository without Compose declares the same things in
`.gregale/project.yaml` at its root
([ADR-20261025093331](adr/20261025093331-project-config-and-whole-project-previews.md)):

```yaml
service_policy: project        # project, declared or account, for every service
services:
  billing:
    visibility: internal       # no public URL; set when the app is created
    depends_on: [ledger]       # billing gets GREGALE_SERVICE_LEDGER_URL
  admin:
    service_policy: declared   # admin may call only what it declares
```

Service names are the ones the plan shows. The file also applies to Compose
services, but Compose stays the more specific source: a Compose
`x-gregale-service-policy` wins, and `depends_on` lists from both are merged.
A project created before the `project` policy became the default switches by
setting `service_policy: project` here. An unknown name, key or value fails
the plan and names the file.

## Configuration

Compose `environment:` and `env_file` entries name the variables a service
needs. The plan lists every one that will have no value in the target
environment under **Needs a value**; set them before the first request:

```sh
gregale env set --app shop-api LOG_LEVEL=info
gregale secrets set --app shop-api STRIPE_KEY=sk_live_…
```

or pass a `--secrets-file` to `gregale deploy`, which supplies its keys to every
selected service.

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
      BILLING_URL: http://billing:8080   # not imported: Docker address
```

The plan lists imported names under **Imported from Compose** and gives the
reason beside each value it skips. Gregale never overwrites a value already set
on the app, never imports secret-looking values, Docker service addresses,
`${…}` interpolation or `env_file` values, and never shows values in the plan.
Imports happen on reviewed deploys (`gregale deploy`, the dashboard,
`--repo … --project`); a GitHub push deploy imports nothing. Values in
`compose.override.yaml` are imported too, so keep local-only settings out of an
opted-in project.
See [ADR-20261023085230](adr/20261023085230-compose-env-import.md).

## Pull request previews

A pull request preview of a service under the `project` policy runs every
service of the project that serves calls, built from the pull request's
branch, so its calls reach the same pull request's siblings
([ADR-20261025093331](adr/20261025093331-project-config-and-whole-project-previews.md)).
Workers, jobs and scheduled services join only when a service lists them in
`depends_on`, so a preview never consumes production work. Each preview
service counts against the plan's pull request preview allowance.

Under the `declared` policy a preview runs the changed service and the services
it lists in `depends_on`. A call outside that set would reach production, so it
is refused by default (`403 preview_production_dependency_denied`). See
[preview environments](preview-environments.md) and [networking](networking.md)
for letting chosen production services accept preview traffic.

## Cold starts and tracing

Parked services wake on their first request, so a chain of cold services wakes
one after another. `gregale wake-ahead <app> on` lets an app start restoring
the services it usually calls while it is still waking itself; see
[wake-ahead](service-wake-ahead.md). Traces follow a request across services;
see [tracing](tracing.md) and the [service map](service-map.md).
