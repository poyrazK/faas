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
(what to add to `uses:`, applied with `gregale deploy`), 401 only for a token whose signature, audience or
expiry fails, and 403 `insufficient_scope` for a correctly signed token whose
caller may not use bindings (a stage deployment, a stopped instance, a deleted
app, an inactive account). Only 401 counts against the shared per-IP
auth-failure limiter, so an SDK on a refused instance retrying on every
credential refresh cannot lock its node's other tenants out of apid. An
Idempotency-Key is scoped to the caller app by hashing it with the caller's
ID (a fixed 72 bytes), so any key the route accepts still fits. The handler then runs as
the target's resource account, exactly as an API key of that account would,
so plan limits, budgets and idempotency apply unchanged. A target slug that
does not exist, or is another account's, answers by the caller's own grants
alone: `no such app` (404) only when `uses:` names that slug, and otherwise
the same 403 `binding_not_granted` an existing ungranted app gets, so a
workload cannot probe which slugs exist.

Producer-route calls are not audited one by one, as API-key sends are not;
the workload credentials of section 3 are, because a key can later be seen
in a bucket's access log: `object_storage.workload_credential_issued` names
the caller app, instance, bucket, access key, scope and expiry (never the
secret), and `object_storage.workload_credentials_reconciled` counts the
keys an apply rescoped or revoked.

A staged release (`gregale deploy --no-traffic`) is a production deployment
of the same app and holds its grants, so a candidate can be qualified
against the real resources before it takes traffic; stage environments do
not.

The platform flag `cross_app_bindings` (decided for the resource account)
gates the token path; off, a binding token is refused as if the route did not
accept one and API keys are unaffected.

### 3. Cross-app object storage through the loopback endpoint (slice 2)

A `buckets` grant gives the caller short-lived S3 credentials, without a
static key in its environment:

- **Issue.** `POST /v1/workload/object-storage/credentials` takes a
  gregale:bindings token (live production instance, flag on), picks the
  caller's grant for the named bucket (or its only bucket grant), resolves
  the owner app (`self`, a service of the caller's project, or an app slug of
  the same resource account) and its production bucket, and mints a
  one-hour credential limited to the grant's access and key prefix.
- **Storage.** The credential lives in its own table,
  `object_storage_workload_credentials`, with `GRGW` access keys (customer
  and compute keys are `GRGA`). Environment clones, credential inventories
  and the per-bucket key cap never see it. The secret is sealed to the
  Storage domain, as for other S3 credentials. apid deletes expired rows
  every five minutes; an expired row stops resolving at once. One instance
  holds at most `api.WorkloadObjectCredentialsPerInstanceBucket` (8) live
  credentials per bucket; the lifetime is `api.WorkloadObjectCredentialTTL`.
- **Revocation.** A credential also stops resolving once its caller app is
  deleted (which covers `project destroy`) or its instance no longer holds a
  VM (parked, stopped, failed, evicted). `project apply` deletes a changed
  app's credentials for a bucket its new `uses:` no longer names. A grant
  whose access or prefix changed **rescopes** the live credentials in place
  instead of deleting them: guest-init and the app's AWS SDK each keep a
  credential until shortly before it expires, so a deleted key would fail
  every call of the running instances for up to an hour (the 2026-11-26 prod
  trial saw exactly that), while a rescoped key keeps serving what the grant
  still allows and is refused the rest within the gateway's two-second
  resolution cache. Credentials the grants still cover are left alone.
- **Exposure.** A credential is a bearer secret like any AWS key: it works
  from anywhere the S3 endpoint is reachable, not only from its VM, until it
  expires or is revoked. The short lifetime, the per-grant scope and the
  issue audit bound what a leaked key can do.
- **Gateway.** `ResolveObjectS3Credential` resolves a `GRGW` key from that
  table and returns it shaped as a compute-binding key (`ManagedAppID` is
  the caller, `KeyPrefix` the grant's prefix), so s3-gatewayd's existing
  permission, key-scope and bucket-settings rules apply unchanged.
- **Guest.** guest-init serves `GET http://127.0.0.1:2773/v1/credentials/object-storage[?bucket=]`
  on its loopback identity listener: it fetches a gregale:bindings token over
  vsock, calls apid (the workload's `GREGALE_API_URL`, https only) and caches
  the answer until five minutes before `Expiration`; apid's refusal of a
  bucket is cached for 30 seconds and reaches the app as its own code with
  apid's explanation (`bindings_disabled`, `binding_not_allowed`,
  `bucket_required` listing the granted buckets, `bucket_not_found`,
  `bucket_not_granted`). The answer is the AWS
  container credential shape (`AccessKeyId`, `SecretAccessKey`, `Token`,
  `Expiration`) plus `Bucket`, `KeyPrefix`, `Endpoint` and `Region`.
- **Env.** `project apply` sets `AWS_CONTAINER_CREDENTIALS_FULL_URI` to that
  URL for a service with exactly one bucket grant and removes the platform's
  value otherwise; a value the customer set is left alone and is not reported
  as drift. AWS SDKs accept a loopback http container-credential URI and
  refresh before expiry. When the platform's URI is set, schedd also hands
  the instance `AWS_ENDPOINT_URL_S3` and `AWS_REGION` (its
  `FAAS_PUBLIC_S3_ENDPOINT` / `FAAS_PUBLIC_S3_REGION`, defaulting to the
  object-storage public endpoint and region) unless the app sets either
  itself, so `new S3Client({ forcePathStyle: true })` (boto3:
  `Config(s3={"addressing_style": "path"})`) is all the code an app needs.
  Path style stays required until the gateway serves virtual-hosted bucket
  names.

A single endpoint serves one credential per bucket. A service with several
bucket grants gets no platform default, because the endpoint without
`?bucket=` cannot pick one; it configures one S3 client per bucket with the
URL plus `?bucket=NAME`.

### 4. Data resources in `project apply` (slice 3)

Already built by ADR-20261106105015: root `databases:` and `buckets:` entries
with `create: true` are planned and created by `project apply` before the
services that bind them, adopted by name when they exist, and never deleted
by a deploy. A bucket created that way is what a `uses.buckets` grant names
(`app:` is the service it is bound to). Redis, realtime endpoints and edge
cache are not yet declarative; they stay follow-ups behind their own flags
(managed Redis is dark).

### 5. `gregale project destroy` (slice 4)

`gregale project destroy <slug> (--dry-run | --yes) [--include-data]` plans
from the delete preview, each app's buckets and Postgres bindings, then:

1. with `--include-data`, deletes each app's buckets. The API deletes only
   empty buckets, so the plan checks each one and keeps a bucket that still
   holds an object, version or delete marker, together with its app and the
   project, naming the commands that empty it;
2. deletes apps that own no remaining bucket (their bindings go with them);
3. with `--include-data`, deletes managed PostgreSQL databases whose every
   binding belonged to the project's apps (deletion protection still
   applies; a database another app uses is kept);
4. deletes the project once no app is left.

Without `--include-data` an app that owns a bucket, the project, and every
database are kept and listed. A refusal keeps what depends on it and the
command exits 1; running it again continues from what is left. It is not a
transaction across providers.

## Consequences

- An app no longer needs an API key to call Gregale on behalf of itself or a
  sibling app; what it may do is reviewable in one file.
- Admin keys in app environments become a lint finding the CLI can point at.
- The token path adds one manifest read per call (the caller app is already
  loaded to check the instance). Slice 2 adds one table of short-lived rows.
- Stage and branch deployments cannot use binding tokens yet.

## Slices

1. `uses:` schema, manifest carriage, `gregale:bindings` audience, queue
   send, realtime publish and delayed tasks accept binding tokens (dark).
2. Short-lived workload S3 credentials (table, apid route, gateway resolve,
   guest-init loopback endpoint, `AWS_CONTAINER_CREDENTIALS_FULL_URI`).
3. Covered by ADR-20261106105015 (`create: true`); Redis, realtime and edge
   cache resources are follow-ups.
4. `gregale project destroy`.
