# ADR-20261105095300: Project deploys fit the deploy budget whole; Compose declares a service's protocol

- Status: accepted
- Date: 2026-11-05
- Amends: ADR-20261028100000 (account deploy rate), ADR-124 (app protocol),
  ADR-576 (private TCP between services)

## Context

A project deploy (`gregale deploy`, `POST /v1/projects`) and a GitHub push
admit each service against the account's hourly deploy limit one at a time.
When fewer deploys remained than the project had services, the first services
deployed and the rest failed: the project ran on two releases, its siblings
called each other across them, and a durable GitHub delivery retried the rest
after the window reset, an hour later, without review.

A gRPC service deployed from Compose stayed `http1`: the project reconcile
never wrote `apps.app_protocol`, and Compose had no way to say it. Its public
gRPC traffic and its siblings' calls went over the HTTP/1 bridge and failed
until the owner ran `gregale app update --app-protocol grpc` on each app.

Private TCP between services (ADR-576) is off by default on every node and
is off in production today, but the networking guide described it as
available.

## Decision

1. A project deploy reads the account's remaining deploys before it admits
   any service. When the services it would deploy need more than remain, it
   deploys none of them and spends none of the budget; each service's result
   says how many deploys it needed, how many remain and when the window
   resets. The plan carries the same warning. A read error falls back to the
   per-service admission.
2. A GitHub push does the same before enqueueing: every project service it
   would deploy gets a failed check with that message, no deployment is
   created, and the delivery completes instead of retrying. App-bound
   repositories deploy one app and keep the bridge's admission.
3. Compose accepts `x-gregale-app-protocol: http1 | http2 | grpc` on a
   service with a deployable workload. Project and push reconciles set the
   app's protocol to the declared value after applying the plan; a service
   that declares none keeps its current protocol. The plan gate refuses a
   protocol the account's plan does not include (gRPC on Free).
4. The networking and multi-service guides say private TCP is off unless the
   operator has enabled it, and that gRPC between services needs only the
   protocol declaration. Tracing and the service map are labelled as the
   previews they are. Enabling private TCP in production stays operator work
   under ADR-576.

## Consequences

- A project never lands partly deployed because the hourly budget ran out.
  A project with more services than the plan's hourly deploys cannot deploy
  all of them in one go; `--only` deploys a subset.
- The budget read and the per-service consumption are not one transaction:
  a concurrent deploy can still take the last deploys between them, and the
  per-service admission then refuses as before. The check removes the common
  case, not the race.
- Removing `x-gregale-app-protocol` from Compose does not reset the app to
  `http1`; the owner changes it with `gregale app update`.

## Alternatives rejected

- Reserving the whole budget in one transaction: needs a new store method and
  a refund path when a later per-service gate refuses; the read-then-consume
  check covers the observed failure.
- Deploying the services that fit, in dependency order: still leaves the
  project on mixed releases.
- Inferring gRPC from a `.proto` file or a port: guesses wrong for services
  that ship protos but serve JSON, and a wrong protocol breaks the service.
