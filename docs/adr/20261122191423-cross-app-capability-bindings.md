# ADR-20261122191423: Cross-app capability bindings and project data resources

- Status: Proposed (dark behind the `cross_app_bindings` platform flag)
- Date: 2026-11-22
- Related: ADR-1141 (workload identity), ADR-1140 (queue pull leases),
  ADR-1383 (service binding CLI configuration), ADR-157 (object storage
  access control), ADR-20261107180000 (one config file),
  ADR-20261116120000 (protection by default), ADR-050 (project object)

## Context

An agent built a three-app product (web, worker, MCP server) on Gregale and
still had to:

1. mint three admin-scoped API keys and put them in `GREGALE_API_TOKEN` so
   each app could call `queues/send`, realtime `publish` and `delayed-tasks`
   on another app (realtime publish needs `deploy:write`, so the key was
   effectively an admin key sitting in an env var);
2. create object-storage credentials for the bucket the web app owns and
   seal copies into the worker and MCP apps, because a bucket credential
   belongs to one app;
3. run 25+ imperative CLI calls to create the apps, the database, the bucket,
   the realtime endpoint and the edge cache, in the right order, and the same
   again in reverse to tear it down.

Gregale already has the pieces for (1) and (2): every instance can fetch a
short-lived RS256 workload-identity token from its loopback endpoint
(`FAAS_WORKLOAD_IDENTITY_ENDPOINT?audience=…`), signed by vmmd for the
instance's account, app, instance and deployment scope; apid already accepts
those tokens for queue pull leases, event acknowledgments, flags, agents and
the Data API. What is missing is a way for an app to be granted an action on
*another* app, and a way to declare that next to the services.

For (3), `gregale project plan|apply` already reconciles the services of a
repository (Compose services and the `project:` block of the root
gregale.yaml) into apps and builds, ordered by `depends_on`. It does not
create data resources and there is no destroy.

## Decision

### 1. `uses:` grants in gregale.yaml

Each service in the `project:` block may declare what it uses:

```yaml
project:
  services:
    web:
      uses:
        queues:
          - app: worker          # project service name, app slug, or self
            queue: transforms    # queue name, or "*" (default "*")
        realtime:
          - app: self
            channels: "room:*"   # channel glob; one trailing * (default "*")
        delayed_tasks: [self]
        buckets:
          - bucket: assets       # logical bucket name
            app: web             # owning app (default self)
            prefix: rooms/       # optional key prefix
            access: read_write   # read | write | read_write
```

The block is parsed strictly (`pkg/capgrant`, no dependencies beyond YAML) and
carried by `project apply` into the app manifest (`manifest.uses`, existing
JSONB, no migration). Each apply replaces the app's grants with what the file
declares. Targets are resolved when a call is made, not when the file is
applied, so a grant may name a service the same apply creates. A target
matches a service of the same project by name or any app of the same resource
account by slug; `self` is the caller.

A grant only ever widens what a workload token can do; it never changes what
API keys can do, and it never lets a token act outside the caller's resource
account (personal account or organization).

### 2. Workload tokens on producer routes

A new audience `gregale:bindings`. These routes accept a JWT-shaped bearer
as a binding token in addition to their API-key chain:

| Route | Grant | Resource checked |
| --- | --- | --- |
| `POST /v1/apps/{slug}/queues/send`, `send-batch` (and `/v1/orgs/…`) | `queues` | queue name from the body |
| `POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/publish` | `realtime` | channel from the path |
| `POST /v1/apps/{slug}/delayed-tasks` | `delayed_tasks` | target app only |

apid verifies the token (signature, audience, expiry), loads the caller app
and requires a live instance of it running a **production** deployment
(queue sends already serve the production binding only; stage callers get
their own grant model when environment branches add identity scoping). The
target app is the one in the path and must belong to the caller's resource
account. The caller's manifest must hold a grant for the target and the
resource. Refusals are 403 `binding_not_granted` naming the missing grant
(what to add to `uses:`), or 401 for a bad token. The handler then runs as
the target's resource account, exactly as an API key of that account would,
so plan limits, budgets and idempotency apply unchanged. Audit records the
caller app and instance as the actor.

The platform flag `cross_app_bindings` (decided for the resource account)
gates the token path; off, a binding token is refused as if the route did not
accept one and API keys are unaffected.

### 3. Cross-app object storage through the metadata endpoint (slice 2)

A `buckets` grant gives the caller short-lived S3 credentials, served by vmmd
at `http://169.254.169.254/v1/credentials/object-storage` in the AWS container
credential shape (`AccessKeyId`, `SecretAccessKey`, `Token`, `Expiration`).
guest-init sets `AWS_CONTAINER_CREDENTIALS_FULL_URI` to it when the app holds
a bucket grant, so stock AWS SDKs pick the credentials up and refresh them
with no code change. The credentials are derived per instance and expire in
one hour; s3-gatewayd checks the bucket and prefix of the grant on every
request. No static key is written into the app's environment.

### 4. Data resources in `project apply` (slice 3)

The `project:` block gains `resources:` (`postgres`, `buckets`, `redis`,
`realtime`) that `project plan` diffs and `project apply` creates before the
services that bind to them, and binds them to the services that list them in
`uses:`. Existing resources are adopted by name; apply never deletes a data
resource that disappeared from the file (it reports it as orphaned).

### 5. `gregale project destroy` (slice 4)

Removes the project's apps, bindings and non-data resources in reverse
dependency order. Data resources (Postgres, buckets, Redis) are detached and
kept unless `--include-data` is passed, and then still need the
protection-by-default confirmation (ADR-20261116120000). Destroy is not a
transaction across providers: it records its plan and is resumable; a second
run continues where the first stopped.

## Consequences

- An app no longer needs an API key to call Gregale on behalf of itself or a
  sibling app; what it may do is reviewable in one file.
- Admin keys in app environments become a lint finding the CLI can point at.
- The token path adds one manifest read per call (the caller app is already
  loaded to check the instance); no new tables.
- Stage and branch deployments cannot use binding tokens yet.

## Slices

1. `uses:` schema, manifest carriage, `gregale:bindings` audience, queue
   send, realtime publish and delayed tasks accept binding tokens (dark).
2. Object-storage credentials from the metadata endpoint.
3. `resources:` in `project plan|apply`.
4. `gregale project destroy`.
