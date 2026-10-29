# Networking

Inspect the effective network contract for an app:

```bash
gregale app APP_ID network show
gregale app APP_ID network doctor
gregale app APP_ID network attach NETWORK_ID --region REGION --cidrs CIDR[,CIDR...]
gregale app APP_ID network detach
```

`network attach` and `network detach` belong to private networks, a preview;
see [private networks](private-networks.md).

## Outbound connections

An app can open TCP connections to public hosts on ports 80 and 443. Pro and
Scale apps can add up to 8 and 32 more ports with
`gregale app egress-ports add <port>`.

Connect by hostname. Gregale's resolver allows the addresses each lookup
returns, for between 10 minutes and an hour, so an HTTP client that looks up
`api.example.com` connects normally. A connection to an IP address the app
never looked up is refused.

An egress allowlist (Hobby and up; 8, 16 or 64 entries by plan) changes both
rules: the listed ranges are reachable without a lookup, on any TCP port but
25, and every other destination is refused, hostnames included. Use it to pin
an app to the services it calls, or to reach fixed addresses, and list every
destination the app needs:

```bash
gregale app egress-allowlist add 203.0.113.10/32
```

Always refused: private ranges (`10.0.0.0/8`, `100.64.0.0/10`,
`172.16.0.0/12`, `192.168.0.0/16`), link-local and cloud metadata addresses
(`169.254.0.0/16`), port 25, and anything but TCP apart from DNS. Ports 465
and 587 reach allowlisted destinations only. DNS always goes to Gregale's
resolver, so DNS over HTTPS or TLS does not work. New connections are
rate-limited per plan.

## Public WebSockets

Enable WebSockets on an app with `gregale app APP_SLUG --websocket-enabled`.
An open WebSocket counts as an in-flight request, so its instance stays
running past the idle timeout for as long as the connection is open.

The public edge closes a WebSocket that carries no frames for about 100
seconds. Send a ping or an application heartbeat at least every 60 seconds,
and reconnect when the connection closes. A connection that carries traffic
stays open.

## Public gRPC

When Gregale itself refuses a gRPC call, before it reaches your app, the
client receives a gRPC status, not an HTTP error page. Examples are a missing
bearer token, a rate limit, a timed-out wake, or maintenance. `grpc-message`
carries the reason and the stable problem code, for example
`Authentication required: … (unauthorized)`.

| Refusal (HTTP) | gRPC status |
|---|---|
| 400, 422 | `INVALID_ARGUMENT` |
| 401 | `UNAUTHENTICATED` |
| 402, 412, 428 | `FAILED_PRECONDITION` |
| 403 | `PERMISSION_DENIED` |
| 404, 410 | `NOT_FOUND` |
| 405, 501 | `UNIMPLEMENTED` |
| 408, 504 | `DEADLINE_EXCEEDED` |
| 409 | `ABORTED` |
| 413, 431, 429 | `RESOURCE_EXHAUSTED` |
| 500 | `INTERNAL` |
| 502, 503 | `UNAVAILABLE` |
| anything else | `UNKNOWN` |

Your app's own gRPC responses, including their `grpc-status` trailers, pass
through unchanged. gRPC-Web requests are not translated.

## Internal services

Apps in the same account reach one another by name, on every plan. There is no
VPC, subnet, security group, internal load balancer, service registry, or DNS
record to configure:

```text
public-api  ──►  auth
            ├─►  billing
            └─►  recommendation
```

New HTTP bindings use port **10081**, which works with standard Node `fetch`.
Port 10080 remains available for previously deployed callers; redeploying a
caller refreshes its generated binding URLs. Both ports use the same caller
identity, account, binding, and target authorization checks. HTTPS bindings
remain an explicit transport choice.

Each dependency is an ordinary app. From `public-api`, call them as:

```text
http://auth.svc.gregale:10081
http://billing.svc.gregale:10081
http://recommendation.svc.gregale:10081
```

The name is the project workload name (or the app slug for a standalone app).
Declare the edges with `depends_on` and Gregale injects the URLs for you, so
nothing hard-codes a hostname:

```yaml
services:
  public-api:
    build: ./public-api
    depends_on: [auth, billing, recommendation]
  auth:
    build: ./auth
  billing:
    build: ./billing
    x-gregale-allow-callers: [public-api]
  recommendation:
    build: ./recommendation
```

`public-api` then starts with `GREGALE_SERVICE_AUTH_URL`,
`GREGALE_SERVICE_BILLING_URL`, and `GREGALE_SERVICE_RECOMMENDATION_URL` in its
environment. The dependency graph is validated before anything deploys —
unknown names, self-edges, and ambiguous names are rejected.

By default, `GREGALE_SERVICE_BILLING_URL` remains the legacy HTTP URL, and
`GREGALE_SERVICE_BILLING_HTTPS_URL=https://billing.internal` is available as
an explicit HTTPS alias. `GREGALE_SERVICE_BILLING_HOST=billing.svc.gregale`
is the bare host for non-HTTP clients (see
[Private TCP between services](#private-tcp-between-services)). A caller can opt into HTTPS-first transport for all
of its declared service bindings:

```yaml
services:
  public-api:
    build: ./public-api
    depends_on: [auth, billing, recommendation]
    x-gregale-service-transport: https
```

With this setting, each canonical `_URL` uses `https://<service>.internal`;
the `_HTTPS_URL` companion remains available with the same value. Plain HTTP
calls from that caller are rejected by the service proxy, so client retries do
not silently downgrade. Enable private HTTPS and workload CA trust on the
compute path before adopting the setting. Omitted transport keeps existing
workloads' stored choice and defaults new workloads to HTTP; set the extension
to `http` to explicitly return to the legacy endpoint. The app API exposes the
same choice as `service_binding_transport` for standalone apps. `gregale
bindings <app>` reports the effective transport.

The same declared edges are exposed as service bindings by the app API and by
`gregale app binding public-api`, alongside database, object-storage, queue,
and outbound integration bindings. New project workloads use the `project`
caller policy
([ADR-20261022111119](adr/20261022111119-project-service-binding-policy.md)):
they may call every service in their own project without listing it, while
calls to services outside the project need `depends_on` and get 403 without
it. A target's `x-gregale-allow-callers` list still applies. Existing apps
retain their persisted policy on reapply; set `x-gregale-service-policy:
project` on a service to move it over, or `declared` to require `depends_on`
for every call. A top-level `x-gregale-service-policy` sets the policy of
every service that sets none, which moves a whole project over in one change:

```yaml
x-gregale-service-policy: project
services:
  web:
    build: ./web
  billing:
    build: ./billing
```

A repository without Compose declares the same policy, per-service
visibility and `depends_on` in `.gregale/project.yaml`
([ADR-20261025093331](adr/20261025093331-project-config-and-whole-project-previews.md));
a Compose declaration wins where both set a policy.

An undeclared sibling has no injected URL; call it at
`http://<name>.internal:10081`
([ADR-20261022160248](adr/20261022160248-project-internal-aliases.md)) or
`http://<app-slug>.svc.gregale:10081`. For an intentional same-account escape
hatch, set `x-gregale-service-policy: account` on the caller. Under the
`declared` policy the CLI reports service bindings as `enforced`; under
`project` and `account` it reports them as `declared`.

`GET /v1/apps/{slug}/bindings` provides the shared binding inventory used by
the CLI and SDKs. It batches app-scoped catalog reads for services, PostgreSQL,
object storage, queues, and outbound integrations. Omit `scope` to inspect all
resource scopes, or use `?scope=staging` / `gregale app binding public-api --scope
staging` to filter database, bucket, and environment queue bindings. Shared
legacy queues and app-wide service and outbound bindings remain included; the filter does not create or isolate
resources for that environment.

The binding inventory separates configuration from queue consumer state and
liveness. An enabled queue binding is shown as `enabled`; its consumer can
still be `not_configured`, `not_observed`, `degraded`, or `stale`. Service
`declared`/`enforced`, database `ready`, and storage credential `active` describe
their configuration or provisioning state, not verified application
connectivity. JSON `runtime_status` is `unknown` unless a queue consumer has a
scheduler observation; `observed_at` identifies that observation, while
`generated_at` is the inventory collection time. `verification_status` is
`passed`, `failed`, `unknown`, or `stale` for service, runtime PostgreSQL,
object-storage, and configured outbound entries.
The optional `verification` contains sanitized stage statuses, the actual task
scope and deployment, completion time, and PostgreSQL credential generation.
Queues and release-only migration bindings remain `unknown`. Queue entries
publish `queue_environment`: an empty string means shared legacy configuration;
otherwise it names the binding's environment. This also distinguishes a project
environment named `app` from the app-wide inventory label. Outbound entries show integration enablement, credential
configuration, and the app binding's allowed methods and path prefixes. They
do not expose provider credentials, resource IDs, or sealed secret names.

`runtime_freshness` separately compares resident app instance start timestamps
with the latest app-wide environment or secret change. It groups live
deployments and deployments with resident instances by deployment and scope.
`serving` counts running instances; `resident` also includes starting, warm,
snapshotting, draining and migrating instances. Each count is `current`
(started after the stamp), `stale` (at or before it), or `unknown` (missing
stamp or timestamp). `starting` is a subset of resident counts. Task guests,
jobs, mirrors, parked and terminal instances are excluded. A deployment is
`stale` while any stale resident remains, then `unknown` if any resident is
unknown, `updating` while a replacement starts, `current` when its remaining
residents are current, and `inactive` when it has none. Scope filtering also
filters this projection. The app-wide stamp conservatively applies to every
binding; these timestamps do not prove guest acknowledgement, readiness,
credential use or connectivity. A passing canary never changes these counts.

Pending PostgreSQL and object-storage rotations may include `refresh`, the
durable rolling restart's correlation `wake_id`, status, attempts and sanitized
failure reason. `not_queued` means no retained outbox record was found, including
rotations staged before handoff or waiting for a later wake. It is not proof
that a restart never happened. `unknown` means status could not be read.
`completed` describes that handoff; it does not override stale runtime counts
or verification. The CLI prints freshness alongside verification and warns
when serving instances predate the configuration change. Freshness or refresh
read failures preserve other results and make inventory incomplete.

The endpoint requires app read access and completed MFA for browser sessions.
PostgreSQL metadata additionally requires `managed-postgres:read`; storage
metadata requires `storage:manage`, matching the existing compute-binding read
surface. An admin key or authenticated session has the existing full access.
Insufficient resource permissions omit that section without reading it.

If a section or consumer status cannot be read, the API returns HTTP 200 with
the available results, `complete: false`, sanitized `warnings`, and structured
`issues` (`type`, `code`, `severity`, `message`). The CLI prints the available
results and warnings in the same document; failed queue status reads remain
`unknown`. Error issues, including denied resource permissions, return CLI exit status 1 after printing the partial
inventory. A gated, unavailable PostgreSQL preview remains a warning by
default; use `gregale app binding public-api --require-complete` to return status 1 for
any incomplete inventory, including that preview. `complete: true` means all
inventory and status reads succeeded, not that every dependency is healthy.
The response is a best-effort projection, not an atomic snapshot, and uses
`Cache-Control: no-store`. An unobserved consumer is not a failed read: its
configuration can be known while its runtime status remains `unknown`.
The command only reads metadata; it does not invoke application handlers or
providers. Use `bindings verify` for service, PostgreSQL or object-storage connection probes.

Use `bindings check` to evaluate those recorded observations in CI:

```sh
gregale app binding verify public-api --all
gregale app binding check public-api --scope production --max-verification-age 10m --json
```

The default check makes one inventory GET and returns exit status 0 when its policy
passes, or 1 for blockers, invalid options or failed reads. It does not create
tasks, invoke providers or restart instances. Omit `--deployment` to use the
current manual-task deployment. Use `--deployment ID|vN` to select an exact live,
materialized deployment, including a candidate receiving zero traffic. Revision
handles resolve within the named app. An explicit `--scope` asserts that the
selected deployment uses that scope; omit it to use the deployment's scope.
App-wide service and outbound bindings and shared legacy queues remain included.
PostgreSQL, object-storage and environment queue bindings in other scopes are
explicitly skipped. PostgreSQL bindings with `access: migration` are reported as
`skipped` with reason `release_only`: their credentials are confined to the
deployment's release task. The existing release-command gate must succeed before
activation; runtime probes, application ACKs and refresh requirements do not
validate migration credentials. `bindings verify --all` skips these bindings and
creates no migration probes.

Use `--wait --timeout 5m --poll-interval 1s` to observe already pending probes,
credential refreshes, runtime replacement or application acknowledgements. Waiting
pins the first selected deployment and scope and prints one final report. It
creates no tasks or refreshes. Terminal failures and missing probe evidence stop
immediately; timeout returns the last blocked report and exit status 1. Ctrl-C
returns 130. Failed reads stop the wait with a nonzero result.

For a staged rollout, verify and check the same deployment before promotion:

```sh
gregale app binding verify public-api --deployment v12 --all
gregale app binding check public-api --deployment v12 --max-verification-age 10m --json
gregale release traffic promote --app public-api --deployment v12 --if-serving v11 --require-bindings --max-verification-age 10m
```

`--require-bindings` makes the server repeat the exact deployment check and
compare binding/configuration, probe and runtime facts inside the traffic
transaction. Blockers, changed facts or evidence that expires while waiting
leave traffic unchanged. The gated promotion API is
`POST /v1/deployments/{id}/promote`; older servers return 404 before mutation.
JSON receipts include `bindings_check`, the applied policy and coverage. Check
failures return structured blockers in the problem envelope. An already
promoted target still requires a current passed check. The default maximum
verification age is ten minutes; `--allow-unsupported` explicitly waives
queue/outbound probe coverage and keeps it partial. These policy flags require
`--require-bindings` on promotion. Promotion creates no probes or restarts.
The gate protects committed platform observations at promotion; later changes
and upstream outages still require monitoring. See [ADR-529](adr/529-binding-gated-promotion.md).

Evidence stays separate per deployment, binding and scope. A passing serving
revision cannot hide a failed or unprobed candidate. The CLI requires the server
to confirm explicit selection and rejects admission or polling receipts from
another deployment or scope. Older servers that ignore the selector cannot
produce a passing result. UUID selection uses one inventory read; revision
selection first resolves the app's deployment ladder. Probes use the selected
artifact with current scoped configuration. A check does not lock configuration
or traffic promotion against subsequent changes.

The default policy requires a complete inventory, ready managed bindings and
passed service, PostgreSQL and object-storage evidence matching the selected
deployment and scope. Evidence must have a completion timestamp no more than
ten minutes old, including the boundary, and must not be in the future. Use a
positive `--max-verification-age` duration to change that window. Missing,
pending, failed, stale or contradictory evidence blocks the check. PostgreSQL
credential generations must also match.

In the selected scope, stale or unknown residents and starting replacements
block the check, including residents from superseded deployments. A fresh
passing task probe cannot hide an old serving instance. An inactive deployment
is allowed with a warning that no resident adoption can be assessed. Pending
credential retirement or an unfinished, failed or unknown refresh handoff also
blocks; a completed handoff does not override pending retirement or runtime
freshness. Inventory read issues block even when their severity is `warning`.

Active queue bindings and outbound bindings without a supported configured probe have unsupported connectivity coverage and
block by default. `--allow-unsupported` explicitly waives only this coverage gap;
the report retains `coverage: partial`, `status: unsupported` and a warning when
consumer readiness has no blockers.
Disabled queue and outbound bindings are skipped. Unknown binding types always
block and require a CLI update. A selected scope with no active bindings reports
`coverage: none` with a warning rather than claiming connectivity coverage.

Enabled push queue bindings also require an active, healthy scheduler consumer
with a nonzero poll timestamp at most thirty seconds old and no later than the
check clock. Missing, paused, degraded, unobserved, unknown or stale consumers
block with `queue_consumer_*` findings, including under `--allow-unsupported`;
their per-binding status is `blocked`. `--max-verification-age` affects probe
evidence only. Gated promotion checks poll expiry again at the traffic write,
and a concurrent consumer change also rejects promotion. Inspect
`gregale queue status APP` and binding status, restore or resume the consumer,
resolve its errors or scheduler clock/polling problems, and retry after a fresh
healthy poll. A healthy idle queue needs no delivered message. External pull
consumers retain partial coverage because their liveness is not observed by the
scheduler. Polling health establishes control-plane readiness; it does not
prove guest queue access or message delivery. See
[ADR-523](adr/523-queue-binding-readiness.md).

A stored release policy can explicitly require scheduler readiness for individual
push queue bindings while retaining strict verification for other dependencies:

```sh
gregale app binding release-policy set public-api --scope production --mode enforce --expected-revision 0 --queue-require orders=consumer_readiness
gregale app binding check public-api --deployment v12 --release-policy --json
```

`orders` is the binding name. The requirement needs exactly one shared or
selected-environment binding, enabled push delivery, and a healthy consumer poll
no older than thirty seconds. Missing, ambiguous, disabled, pull, unobserved,
stale and unhealthy consumers block. The queue environment must be confirmed by
the server; older inventories cannot satisfy this requirement. JSON exposes
`queue_requirements` and `required_queue_evidence`. A satisfied readiness policy
returns `passed` with `coverage: partial` and a `queue_connectivity_unverified`
warning. It establishes scheduler readiness only; it sends no messages and does
not establish guest connectivity or end-to-end delivery. Unlisted queues and
unsupported outbound bindings still block stored enforcement. Probe age never
extends the separate queue-poll deadline, which promotion checks under its
transaction locks.

Use `--wait` to observe the next scheduler poll for an active consumer whose
required readiness is stale or unobserved. Waiting sends no messages and does
not resume paused consumers or repair missing bindings.

Use `--queue-require NAME=consumer_readiness` repeatedly (up to 32 bindings) to replace the complete
queue requirement map. Updates require the observed policy revision; use
`--clear-queue-requirements` to explicitly remove all queue requirements.
Existing storage requirements must be retained with `--object-storage-require`
or explicitly cleared. CLI and SDK writes use the dedicated
`PUT .../bindings/release-policy-with-queues` route, so older servers return 404
before mutation. Older write routes reject implicit removal of these
requirements. A local `bindings check --queue-require ...` reads the same
readiness evidence without changing the stored policy. See
[ADR-1346](adr/1346-binding-runtime-audiences-and-queue-release-requirements.md).

To prepare all dependencies for an enforced release policy with one command,
preview the plan and then run it for the same candidate:

```sh
gregale app binding verify public-api --deployment v12 --release-policy --dry-run --json
gregale app binding verify public-api --deployment v12 --release-policy --wait-timeout 5m --json
```

The policy need not contain storage requirements. The plan covers services,
runtime PostgreSQL, object storage, configured outbound integrations and required
queue readiness. Each entry exposes an `action`: `probe` for missing, failed,
stale or insufficient evidence; `reuse` for current evidence; `wait` for an
already-pending compatible probe; `observe` for queue readiness; `skip` for disabled,
release-only or other-scope bindings; and `blocked` for unsupported or invalid
configuration. `observed_check` contains initial findings, while `plan_blockers`
explain conditions that stop the whole batch before tasks. A dry run reports
`status: planned` and `passed: false`; it performs no probes or provider calls.

If any scheduled storage operation writes, add `--object-storage-write` after
reviewing the plan. Without that consent, no part of the batch runs. Current
write evidence can be reused without writing again. The storage requirement and
binding permission select listing or upload/cleanup, with download for a
read/write canary. Unlisted write-only storage bindings also need an explicit
write canary because their credentials cannot perform a listing.

The command pins the deployment, scope and original policy; reconfirms selected
binding configuration before each task and after each successful probe; and
stops on failures or observed configuration changes. It finishes with fresh
policy-aware preflight, observing pending probes, required queue polls and
supported runtime/ACK progress under one overall `--wait-timeout`. Paused or
missing consumers and other terminal failures stop immediately. Reused evidence
that expires before the final check cannot pass. Queue coverage remains partial,
and canaries do not establish resident application adoption. JSON retains the
plan and completed results alongside `preflight` when later work fails. Deadline
errors use `error_code: verification_timeout` and retain any admitted task ID.

The final result is an observation; traffic promotion rechecks policy and
evidence under its transaction locks. See
[ADR-1347](adr/1347-unified-binding-policy-verification.md).

Human output identifies blockers and remediation. JSON includes `passed`, the
applied policy, scope and deployment, per-binding coverage, runtime counts,
inventory issues and stable blocker codes such as `verification_expired`,
`verification_scope_mismatch`, `runtime_stale`, `rotation_pending` and
`verification_unsupported`. On a control-plane host that does not run binding
probes (deployment-attached app tasks disabled), missing or stale evidence is
reported as `verification_unavailable` instead of asking for `bindings verify`;
`--allow-unsupported` waives it like an unsupported probe, and a recorded
failure still blocks. This is a point-in-time policy result from a
best-effort projection. It does not prove application readiness or resident
credential use; object-storage evidence covers bucket-list read access only.

Customer-sealed outbound integrations can opt into verification by declaring a
provider endpoint safe to call:

```sh
gregale app binding probe-policy INTEGRATION_ID --path /health --method GET --expect-status 200
gregale app binding verify public-api --deployment v12 --outbound INTEGRATION_ID
gregale app binding check public-api --deployment v12
gregale release traffic promote --app public-api --deployment v12 --require-bindings
```

The method must be GET or HEAD, the canonical path must fit the integration's
route policy, and the expected status must be 2xx. This declaration sends no
provider requests. `bindings verify --all` includes configured outbound probes;
use `bindings probe-policy INTEGRATION_ID --delete` to remove a policy. Calls
use the candidate task's workload identity through Gregale's outbound gateway, with normal
credential injection, route restrictions and admission budgets. Probes bypass
response caching and never include provider bodies, headers or credentials in
reports. Evidence becomes stale when credentials, probe policy, binding policy
or integration configuration change. A failed configured probe remains a
blocker even with `--allow-unsupported`.

Probes use normal TLS verification. Operator-environment credentials, missing
probe policies and installations without an outbound probe gateway keep
unsupported coverage. A successful probe establishes only the configured
endpoint. See
[ADR-522](adr/522-outbound-binding-verification.md).

Each declared outbound dependency can carry a request deadline and retry
policy. For a project workload, put it beside `depends_on`:

```yaml
services:
  public-api:
    build: ./public-api
    depends_on: [billing]
    x-gregale-service-reliability:
      billing:
        timeout_ms: 1500
        max_attempts: 2
        min_remaining_ms: 200
        retry_budget_percent: 10
```

The timeout covers routing, a cold wake, forwarding, and any retry after caller
authorization. An earlier caller deadline takes precedence. `max_attempts: 1`
disables proxy replay for that dependency; omitted settings keep platform
defaults. Retries still require a safe method or an explicit
`allow_non_idempotent: true`, a replayable body, enough remaining time, and an
available aggregate retry token. Only transport-stale attempts are retried.
Policies can name only services in `depends_on`; on reapply, an omitted extension
retains stored policies and removing a binding removes its policy. A WebSocket
handshake uses the deadline, but an established session is not cut off by it.

The target can independently restrict who calls it with
`x-gregale-allow-callers`. In the example, `billing` admits `public-api` but
not other same-account apps, even if they declare a dependency on `billing`.
An omitted list preserves legacy same-account reachability; `[]` denies every
internal caller. The list accepts at most 100 logical app slugs, not generated
PR preview names. In a project, a name in the list (or in
`x-gregale-allow-call-scopes`) is a Compose service of the target's project,
even when that service has a project-prefixed slug
([ADR-20261022120205](adr/20261022120205-project-prefixed-app-slugs.md)). An
app outside the project matches by its slug only when no service of the
target's project has that name. This target check runs before routing or waking `billing`. Preview callers
must also pass the existing project and target preview policies.

For least-privilege access, a target can grant each caller only specific HTTP
methods and path prefixes:

```yaml
services:
  billing:
    build: ./billing
    x-gregale-allow-call-scopes:
      public-api:
        methods: [GET, POST]
        path_prefixes: [/v1/invoices, /v1/checkout]
```

When `x-gregale-allow-call-scopes` is present, it also acts as a caller
allowlist: callers missing from the map are denied. If
`x-gregale-allow-callers` is also set, both policies must allow the caller.
Configured methods are normalized to uppercase and matched against the request
method exactly; `*` explicitly permits every method. A path prefix matches the
exact path and its slash-delimited descendants, so
`/v1/invoices` allows `/v1/invoices/42` but not `/v1/invoices-archive`. Query
strings do not affect the path check. Ambiguous paths such as dot-segments or
duplicate separators fail closed, and the check runs before endpoint lookup or
wake. The platform-owned `bindings verify` probe remains available because it
does not invoke an application route.

Standalone apps use the same target-side policy in `allowed_service_call_scopes`
on `POST /v1/apps` or `PATCH /v1/apps/<slug>`. PATCH omission keeps the current
map, `null` clears it, and `{}` denies every caller. Project-managed apps must
set `x-gregale-allow-call-scopes` in source; they reject this PATCH just like
the existing caller allowlist.

For an app created outside a project, use the
[service binding CLI](service-binding-configuration.md) to preview and apply
these fields from a JSON policy file. The app API is also available: include
`"allowed_service_callers": ["frontend"]` in `POST /v1/apps`, or PATCH
`/v1/apps/customer-billing` with that field to replace the list. PATCH with `[]` denies
all internal callers; PATCH with `null` restores same-account access. Omission
leaves the current policy unchanged. Project-managed and preview apps cannot
change this field through PATCH; edit `x-gregale-allow-callers` in the source.

Standalone callers can declare outbound targets without a Compose project:

```json
{
  "service_binding_targets": ["billing", "identity", "email"],
  "service_binding_policy": "declared",
  "service_reliability": {
    "billing": {"timeout_ms": 1500, "max_attempts": 1}
  }
}
```

Send these fields on `POST /v1/apps` or `PATCH /v1/apps/frontend`. Gregale
injects `GREGALE_SERVICE_BILLING_URL`, `GREGALE_SERVICE_IDENTITY_URL`, and
`GREGALE_SERVICE_EMAIL_URL` into `frontend`; `GET /v1/apps/frontend` reports
the generated `service_bindings`. Under `declared`, the gateway rejects any
other internal target before waking it. Existing standalone apps stay on
`account` reachability unless they opt in; adding targets alone is discovery
only. A PATCH with `service_binding_targets: []` clears the bindings (and
denies every internal target if the policy is `declared`); setting
`service_binding_policy: "account"` restores same-account access. Target names
may be forward references, but a call cannot succeed until the target app
exists. Authorization changes take effect at the gateway immediately; new URL
environment variables appear when the caller next starts or redeploys, while
already-running instances retain their current environment. The generated
`GREGALE_SERVICE_*_URL` namespace is platform-owned.
For `service_reliability`, PATCH omission retains the map, `null` or `{}`
clears it, and an object replaces it. A policy must name one of the app's
declared `service_binding_targets`. Removing a binding also removes its
stored policy when the reliability map is omitted from the same PATCH.
Project-managed and preview apps reject changes to these fields on PATCH; edit the
project source instead. Binding declarations do not expose services publicly.

Bound services can also be called through the short private alias
`http://billing.internal:10081`. Gregale's node-local DNS answers that name
only for a VM whose app declares a `billing` binding, or, under the `project`
service policy, whose project has a `billing` service
([ADR-20261022160248](adr/20261022160248-project-internal-aliases.md)). A
direct request to the service proxy with `Host: billing.internal` gets the same
check, so bypassing DNS cannot grant access. The existing same-account,
target allowlist, and preview checks still run before any target is woken.
Unbound `.internal` names are passed to the configured upstream DNS resolver;
Gregale does not claim the customer's entire private namespace. The existing
`*.svc.gregale` endpoint and default generated URLs remain unchanged for
rolling-upgrade compatibility; callers may opt into HTTPS-first URLs after the
listener and trust are ready. The private HTTPS listener is not enabled on
every installation. Where it is, guest-init builds a per-workload bundle in `/tmp`, exports
`GREGALE_SERVICE_CA_BUNDLE`, and configures common TLS clients (`SSL_CERT_FILE`,
Python Requests, and curl) to trust the private endpoint when the image includes
a standard system CA bundle. Node receives the CA through its additive
`NODE_EXTRA_CA_CERTS` setting. No image or global OS trust store is changed.
Other TLS stacks can explicitly use `GREGALE_SERVICE_CA_BUNDLE` with their own
verifier. For example, `curl https://billing.internal/` and
`requests.get("https://billing.internal/")` verify TLS normally; verification
is never disabled. The raw CA also remains available at
`/etc/faas/service-proxy-ca.crt` for explicit client-specific selection.
The legacy generated binding URLs remain unchanged. See [ADR-274](adr/274-additive-https-service-binding-urls.md)
for the explicit HTTPS canary variable, [ADR-272](adr/272-private-https-service-bindings.md)
for listener rollout, and [ADR-273](adr/273-workload-scoped-service-ca-trust.md)
for guest trust and CA requirements. See [ADR-275](adr/275-https-service-binding-canary.md)
for caller-side verification.

`gregale app binding verify <app> <service>` runs a platform-owned HTTPS canary
inside a disposable task guest attached to the caller's deployment. Use
`gregale app binding verify <app> --all` to check every declared service before
switching the caller to HTTPS-first transport. The all-bindings form also
checks managed PostgreSQL and object-storage bindings in the scope selected for manual app tasks,
reported as `verification_scope` in the inventory. Other resource scopes and
unsupported binding families are not probed by this command. The form reports
each result and exits nonzero if any check fails. Both forms check the
`.internal` DNS alias, verify the gateway certificate against the
workload-scoped CA bundle, and exercise the same binding and target
authorization path as a real request. The final routing check only consults
the healthy endpoint registry: it does not wake an idle target or invoke its
handler. The probe has no HTTP fallback.
By default the generated URL remains the legacy HTTP endpoint. [ADR-274](adr/274-additive-https-service-binding-urls.md)
keeps the explicit HTTPS canary companion, while [ADR-276](adr/276-https-first-service-binding-transport.md)
lets a caller opt into HTTPS as the canonical `_URL`. See [ADR-272](adr/272-private-https-service-bindings.md)
for listener rollout, [ADR-273](adr/273-workload-scoped-service-ca-trust.md) for guest trust, and
[ADR-275](adr/275-https-service-binding-canary.md) for caller verification. The alias is never a public ingress
hostname.

To test an app's managed PostgreSQL binding, run
`gregale app binding verify <app> --postgres DATABASE_URL` with the binding's
environment key. The CLI confirms that the key belongs to a managed database
binding, then starts a bounded platform task in the app's live deployment. The
task reads the injected URL inside the guest, checks URL configuration and
database connection, and runs only `SELECT 1`. It reports the environment,
configuration, connection, and query stages; the URL, host, username, password,
and database response are never included in the report. This confirms basic
connectivity and query permission, not application-specific schema readiness.

Platform service, managed PostgreSQL and object-storage canaries admitted through the app-task
API carry a server-selected binding revision pin stored atomically with the
task. Inventory derives typed outcomes from the durable task result, without
returning its raw output, free-form details, or errors. A malformed, truncated,
pending, cancelled, or unmanaged task cannot supply a passing result. The
latest admission wins even when an older task completes later. A caller
deployment change, binding replacement, credential generation change, or app
runtime-configuration change marks prior evidence `stale`. Unsupported or
unrecorded results remain `unknown`.

`gregale app binding verify <app> --object-storage GREGALE_S3_ASSETS` checks the
six injected S3 environment variables and makes one signed, path-style
`ListObjectsV2` request with `MaxKeys=1`. It has a five-second deadline, does
not retry or follow redirects, and caps provider response XML at 64 KiB.
Empty buckets pass. The report separates environment, configuration,
connection, authorization and bucket access, and excludes endpoint URLs,
credentials, object names and raw provider errors. It never writes objects;
a pass establishes bucket-list read access, not write permission or successful
object downloads. `HeadBucket` alone would only check Gregale gateway access,
without testing the storage provider.

Object-storage evidence uses internal binding and latest rotation identities,
plus the app configuration stamp. Credential cutover invalidates older evidence
even before rotation stamps the app configuration. Rotation cleanup preserves
that identity. Object-storage metadata and evidence require `storage:manage`
or admin, matching the existing compute-binding inventory permission boundary.
See [ADR-526](adr/526-object-storage-binding-verification.md).

The CLI rejects a passing result when task admission selected a different
deployment or scope from the inventory selection. Batch results include the
actual task scope so a deployment change cannot silently mix environments.

`bindings verify --all` reports every inventory binding. Unsupported queue probes
and outbound bindings without probe configuration have explicit reasons;
disabled bindings and resources outside the deployment scope are marked skipped.
JSON includes `total`, `checked`, `passed`, `failed`, `unsupported`, `skipped`,
`not_checked` and `coverage` (`complete`, `partial` or `none`). A supported probe
failure, incomplete inventory, unknown binding type, unfinished probe or absence
of supported probes returns nonzero. Known unsupported bindings retain partial
coverage even when supported probes succeed with exit status 0. Use `bindings
check` to enforce coverage and its explicit unsupported waiver policy.

Service probe evidence includes private dependency revisions for target identity,
service authorization, project preview policy and test-run membership. Changes
invalidate service evidence and binding-gated promotion approvals together. This
conservatively invalidates service probes across the account; target metadata and
revision tokens stay private. Run verification again after such changes. See
[ADR-596](adr/596-service-binding-dependency-evidence.md).

Verification is point-in-time connectivity from a fresh task guest, not an
acknowledgement that resident application VMs adopted current credentials.
The GET inventory remains read-only. Explicit `bindings verify` tasks can
consume compute, establish database connections and issue bucket-list requests; service canaries do not
wake targets or call customer handlers.

Calls are authorized by the platform, not by your code. The caller is
identified from the network identity of the calling VM, so a guest cannot
claim to be another app, and the proxy only permits calls between apps in the
same account. Cross-account calls are refused.

### Keep client and service revisions consistent

Enable an app's compatibility window with `revision_pin_ttl_seconds` (up to
604800). A successful response includes `X-Gregale-Revision: <deployment-id>`.
Clients that cannot upgrade immediately can send that ID back on later public
requests. A replaced revision receives 0% normal traffic but remains reachable
by its exact pin until the cutover TTL expires. A release set can independently
keep that deployment reachable, even after its direct revision pin expires;
when the set is replaced, its own TTL begins. An expired or foreign pin is
rejected; it never silently lands on newer code. This works for HTTP requests
and WebSocket reconnect handshakes, not already-open connections.
The default CORS policy exposes both pin response headers. If you configure a
custom CORS rule, include `X-Gregale-Revision` and `X-Gregale-Release` in its
exposed headers and permit them as request headers for browser clients.

For a static SPA, the gateway sets a host-only `__Host-gregale_release` cookie
on document navigation when revision pin retention is enabled and an active
release graph was selected. The Node browser adapter reads it at initialization
and adds the release header to configured managed origins, including
cross-origin APIs. Gregale strips this platform cookie before forwarding the
request to the app. If a CDN sits in front of the app, preserve the document
response's `Set-Cookie` header with its body; the cached body and cookie must
describe the same release. The cookie is a routing identifier, not a secret.
It is a browser session cookie, while the graph's server-side TTL controls its
routing eligibility. If the graph expires, requests fail with 410 and are not
retried against the active graph.

Same-host browser WebSocket reconnects also inherit this cookie: the browser's
native `WebSocket` API cannot set a custom release header, so the gateway reads
the cookie only from a WebSocket handshake, validates the graph and TTL, then
strips the cookie before forwarding. The SPA and WebSocket endpoint must use
the same hostname for the host-only cookie to be sent. Already-open sockets
stay on their selected deployment until they disconnect.

For a browser socket on a separate managed API hostname, use the Node SDK's
browser adapter `webSocket(url, protocols)` helper after it has learned the
release or has been seeded from SSR/bootstrap. The helper adds the reserved
`gregale.release.<release-uuid>` WebSocket subprotocol; the gateway consumes it
before the app handshake, preserves application subprotocols, and filters the
reserved token from the guest response. The release is not placed in the URL.
The target origin must be included in `managedOrigins`, and opening a managed
socket before the adapter knows a release fails instead of silently routing to
the active graph. Plain native `WebSocket` calls and non-browser clients need a
same-host bootstrap cookie or another explicit pin mechanism. This carrier is
for the handshake only; an established socket stays on its selected deployment
until it disconnects.

For a multi-workload project, publish a complete release set after all member
deployments are ready. An incompatible new service deployment can be deployed
with an explicit 0% traffic weight first; publishing the new graph activates
it for release-pinned calls without shifting the ordinary weighted route:

```http
POST /v1/projects/shop/environments/production/release-sets
Content-Type: application/json

{"ttl_seconds":3600,"deployments":{"shop-api":"API_DEPLOYMENT_UUID","shop-billing":"BILLING_DEPLOYMENT_UUID"}}
```

The response contains a release UUID. Production project ingress follows the
active set by default; a client can continue an older set with
`X-Gregale-Release: <release-uuid>`. Gregale returns that header and sends it
to the API guest. Propagate it on outbound managed service calls; the Go,
Node, and Python SDKs provide request-context middleware/transports for this.
They strip `X-Gregale-Revision` on those downstream hops because revision IDs
are scoped to the caller app. The service proxy verifies the calling VM's
deployment belongs to the release and picks the matching target deployment;
it cannot be spoofed into selecting a graph from a caller header alone. When
the same API deployment belongs to several unexpired sets, an internal call
without the release header returns 409 rather than guessing. The active set
does not expire; its TTL starts when a new set replaces it. Publish a new
complete set whenever project membership changes.

Release sets pin public HTTP/WebSocket handshakes, managed HTTP service calls,
and durable invocations (async invoke, delayed tasks, queues, inbox messages,
and asynchronous edge routes). For durable work, Gregale captures the active
release or an explicit pin when accepting the row, then checks it again before
delivery. A queued item whose release expires fails rather than switching to
new code; schedule it within the compatibility window. The control-plane
invocation, queue, and task APIs accept the same headers on the HTTP request;
invoke and task JSON envelopes may also carry them in their `headers` object.
Accepted control-plane responses return the captured revision or release header
when a version was selected at enqueue time.
Calls that bypass `*.svc.gregale` still need their own release-context
propagation and are not covered by this guarantee. A guest must forward the
received `X-Gregale-Release` on each managed outbound service call when its
deployment can belong to multiple live release sets.

### Inspect a project's release graph

```sh
gregale environment get shop production
gregale environment get shop production --json
gregale environment release-sets shop production --limit 20
```

`inspect` compares active release-set membership with each workload's ordinary
live deployment, which is what its stable environment URL currently selects.
It reports different selections and project membership gaps explicitly. A bare
`gregale environment get` uses the linked project and selected
environment; supplying both positional arguments overrides that context.

The overview includes the configuration version, last promotion, and managed
PostgreSQL/object-storage binding metadata. It omits configuration and variable
values, secret names, and secret fingerprints in both text and JSON. Service
and queue bindings are outside this first overview's binding coverage. Runtime
health is reported as `not_checked`: a live deployment record alone does not
prove health, and inspection does not wake parked workloads. Failure to read
promotion history appears as an issue while the available environment inventory
remains visible. Failure to read environment or release state fails inspection.

`release-sets` lists active, retired, and expired graphs, newest first. The
page size defaults to 50, with a maximum of 100. Pass `next_before` back through
`--before` to continue. Historical visibility does not extend a graph's routing
eligibility or retain deleted artifacts.

### Qualify a staged release before protected promotion

Protected-target promotion from an active source release set is gated on the
latest passing `health` and `smoke` qualification for that exact release-set
ID, non-secret configuration version/hash, and per-workload secret revision
fingerprints. Receipts expire after 24 hours; publishing a new release set,
changing source configuration, or rotating a source secret requires new
checks. `qualify` snapshots the active graph, configuration identity, and
secret revision metadata,
resolves each member's exact deployment preview URL, and runs both configured
GET probes before recording the receipt. The API rejects the result if either
the release set, configuration, or secret revisions changed while probes were
running. Fingerprints cover secret key/version metadata and managed credential
generations only; they never contain secret values or value hashes. Add
`gregale-qualification.yaml` to the project repository:

```yaml
version: 1
timeout_seconds: 5
workloads:
  api:
    health_path: /healthz
    smoke_path: /ready
  billing:
    health_path: /healthz
    smoke_path: /ready
```

Then run:

```sh
gregale environment qualify shop staging --profile gregale-qualification.yaml
gregale environment preview shop --from staging --to production
gregale environment preflight shop --from staging --to production \
  --profile gregale-qualification.yaml --json
```

`preflight` runs the source qualification and promotion preview together. It
exits non-zero when probes fail, the qualification is stale/unresolved, or the
server says promotion is blocked. Its JSON output contains the receipt summary
and blockers, but omits secret-revision fingerprints and the opaque promotion
token. The [GitHub Actions environment-preflight Action](../.github/actions/environment-preflight/README.md)
uses a five-minute OIDC bearer with only `project_environments:read` and
`project_environments:qualify`; the later promotion command still rechecks
policy and the latest qualification on the server.

The profile must define exactly the workloads in the active release set. Each
probe is an HTTPS GET with normal TLS verification; redirects and non-2xx
responses fail. The receipt binds each outcome to its deployment ID and records
the tested configuration version and canonical hash, plus opaque per-workload
secret revision fingerprints. It stores only the
workload, status, HTTP status, and a bounded error code—never response bodies,
headers, configuration values, or secrets. A failed or expired receipt, or one
whose configuration or secret revisions are stale, blocks promotion until a
newer passing qualification is recorded. Promotions without an active source
release set retain their existing behavior.

The read API uses the same project/environment scope as publication:

- `GET /v1/projects/{slug}/environments/{environment}/release-sets`
- `GET /v1/projects/{slug}/environments/{environment}/release-sets/active`
- `GET /v1/projects/{slug}/environments/{environment}/release-sets/{release}`

An active lookup returns 404 when no active set exists. The environment-state
response instead returns `release_set_status: none` and `active_release_set:
null`. When a graph exists, state includes `release_set_status: active`, the
complete stored graph, and each workload's `app_id` for joining membership.
Existing workload `release` fields continue to describe ordinary live selection.
All release inventory reads require the owning account's read access.

### Smoke-test a downstream deployment

`gregale app binding verify` checks the private transport and current route
availability without invoking customer code. To exercise a handler on one
exact deployment, use an explicit smoke request from the caller:

```bash
gregale app binding smoke public-api billing \
  --caller-deployment v12 \
  --target-deployment TARGET_DEPLOYMENT_ID \
  --path /health \
  --expect-status 200
```

Find the target ID with `gregale release traffic status billing`. `--caller-deployment`
selects an exact app-owned live caller deployment, including a candidate at 0%
traffic; `v12` resolves within `public-api`. Omit it to retain automatic caller
selection. `--deployment` remains an alias for `--target-deployment`; conflicting
aliases are rejected. Neither selector changes traffic. This command sends one GET
over the caller's verified `https://billing.internal` binding and accepts any
2xx response by default; `--expect-status 204` requires one exact status. It
does not follow redirects, retain the response body, or include the request
query in its report. This is an active smoke test: a parked selected deployment
is woken before forwarding, so its handler may have application-level side
effects. Use `bindings verify` for a no-wake infrastructure preflight.

The CLI confirms the admitted caller deployment and scope on every task read.
Success requires a complete matching guest report, the expected HTTP status,
and a successful task with exit code zero. Truncated, malformed or mismatched
reports fail. The test exercises the target handler from the caller's task
runtime; it does not execute the caller's own application handler or count as
binding-verification evidence. Ctrl-C requests bounded task cancellation;
`--wait-timeout` bounds CLI reads and waiting, while the task retains its own
90-second execution limit.

The equivalent raw request sends that same target override explicitly:

```bash
curl -H 'Gregale-Target-Deployment: DEPLOYMENT_ID' \
  'https://billing.internal/health'
```

This works for a live deployment at 0% traffic: the service proxy wakes that
exact deployment if needed. The override wins over `Gregale-Version-Key` for
this one service hop, but the version key remains available to the target app. The override header
is removed before forwarding, so it cannot accidentally pin a later call to
another service. Only deployments belonging to the authorized target app are
accepted; malformed IDs return 400 and non-live or wrong-app IDs return 422
instead of silently falling back to weighted routing. Direct public smoke
tests should use the deployment's preview URL instead. Gregale strips this
header from public requests before they reach your app; attach it explicitly
to the service call rather than forwarding it from an end-user request.

### Preview-to-production service policy

A pull-request preview first resolves a service to a preview workload in the
same account, project, and PR. It never selects a preview from another PR,
project, or account. The target preview's protocol and WebSocket settings are
used exactly as they are on the public edge.

Preview provisioning creates the bound app and the transitive `depends_on`
workloads present at the PR head. When the bound app's service policy is
`project`, it also creates every other callable workload of the project:
workers, jobs and scheduled workloads join only as declared dependencies
([ADR-20261025093331](adr/20261025093331-project-config-and-whole-project-previews.md)).
A dependency that is absent from that set can still resolve to the production
app, so the gateway applies the project's production-dependency policy.

All projects use `preview_service_policy: deny` by default. A denied call returns
`403 application/problem+json` with code
`preview_production_dependency_denied` before the proxy discovers or wakes the
target. Legacy `allow_marked` rows are migrated to `deny`. To configure the
policy from a checkout, run:

```bash
gregale github setup public-api --preview-service-policy deny
```

Set `allow_marked` only when the production dependency is designed to receive
preview traffic. A preview of `public-api` calling `billing` then reaches
production `billing`, and any side effects are real.

In `allow_marked` mode, a production service can independently refuse preview
calls with `x-gregale-preview-calls: deny` on its Compose service:

```yaml
services:
  billing:
    build: ./billing
    x-gregale-preview-calls: deny
```

The gateway checks the target's policy before waking or forwarding it and
returns 403 to a preview caller. The target policy defaults to `allow`, so it
preserves existing behavior; the project-level `preview_service_policy` can
still deny all preview-to-production calls. The app API and scan plan show the
effective `preview_service_calls_policy`, and target-policy rejections count
under `gateway_service_call_total{outcome="preview_denied"}`.

Every forwarded request from a preview app carries these markers, whether the
selected target is another preview or an allowed production dependency:

```text
X-Faas-Caller-Env: preview
X-Faas-Caller-Preview-Of: public-api
```

Both headers are platform-owned: anything a workload sends under those names is
stripped before the hop, so the marker cannot be forged. Production callers
carry neither header, so a service that ignores them is unaffected.

Use them to skip irreversible work, tag writes as test data, or refuse the call
outright.

### Verifying the caller (preview)

By default a service learns who called it from platform-set headers. Signed
caller assertions let the target verify that claim itself. They are a preview
and are not enabled on every installation.

Every internal call then carries `X-Faas-Caller-Assertion`: a short-lived
(30 s) EdDSA JWT stating `sub` = calling app id, `aud` = receiving app id,
plus the account and calling instance. The audience binding is what stops a
service replaying an assertion it received against a sibling service.

The header is platform-owned and stripped from anything a workload sends, so it
cannot be forged. It is additive: nothing rejects a call for lacking one, and a
signing failure forwards the call unsigned rather than dropping it.

Guest workloads can fetch the public-only JWKS from
`https://<api-origin>/v1/service-caller-keys` without an API token. The endpoint
is rate-limited, contains no tenant metadata, and publishes only Ed25519 public
keys. Responses are cacheable for five seconds and must be revalidated
afterward; refresh immediately when a token names an unknown `kid`. During
node key rotation, the previous key stays published for the assertion's
30-second maximum lifetime so requests already in flight remain verifiable.

Verification must check the signature, issuer `gregale.svc`, audience equal to
the target app's platform-injected `FAAS_APP_ID`, and the token time window.
For Go `net/http` services, `pkg/servicecaller` provides middleware with a
five-second JWKS cache, single-flight refresh, and an immediate refresh when a
new signing-key ID appears:

```go
middleware, err := servicecaller.NewHTTPMiddleware(servicecaller.HTTPMiddlewareOptions{
    JWKSURL:  apiOrigin + "/v1/service-caller-keys",
    Audience: os.Getenv("FAAS_APP_ID"),
    Require:  true,
})
if err != nil {
    log.Fatal(err)
}
mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
    if caller, verified := servicecaller.VerifiedCaller(r.Context()); verified {
        log.Printf("verified internal caller: %s", caller.CallerAppID)
    }
    w.WriteHeader(http.StatusNoContent)
})
if err := http.ListenAndServe(":8080", middleware.Wrap(mux)); err != nil {
    log.Fatal(err)
}
```

Node services can use the same verification contract from `@gregale/sdk-node`:

```ts
import {
  createServiceCallerVerifier,
  SERVICE_CALLER_ASSERTION_HEADER,
} from '@gregale/sdk-node';

const apiOrigin = 'https://api.gregale.dev'; // use your deployment's API origin
const verifier = createServiceCallerVerifier({
  jwksUrl: `${apiOrigin}/v1/service-caller-keys`,
  audience: process.env.FAAS_APP_ID ?? '',
  require: true,
  onFailure: (error) => console.warn('service caller verification failed', error.code),
});

async function handle(req, res) {
  // Consume the platform-owned header before handing the request downstream.
  const rawAssertion = req.headers[SERVICE_CALLER_ASSERTION_HEADER.toLowerCase()];
  delete req.headers[SERVICE_CALLER_ASSERTION_HEADER.toLowerCase()];
  let caller: Awaited<ReturnType<typeof verifier.verifyHeader>>;
  try {
    caller = await verifier.verifyHeader(rawAssertion);
  } catch {
    res.statusCode = 401;
    return res.end('service caller assertion required or invalid');
  }
  if (!caller) {
    res.statusCode = 401;
    return res.end('service caller assertion required or invalid');
  }
  if (caller.callerAppId !== 'frontend-app-id') {
    res.statusCode = 403;
    return res.end('caller is not allowed');
  }
  req.serviceCaller = caller;
  return routeRequest(req, res);
}
```

`verifyHeader` returns only authenticated identity; with `require: false` (the
default), absent or invalid assertions return `undefined`. `require: true`
throws on either case so the service can reject the request. In both modes,
the application still owns its allowlist and business authorization. The
verifier uses Node's built-in Ed25519 support and does not add a runtime
dependency.

`Require: false` is the default rollout posture: missing or invalid assertions
do not block a request, and are never added to its context as verified
identity. Set `Require: true` only after assertion signing and key publication
are enabled on every node that may originate calls; required mode returns 401
if the assertion is missing, invalid, expired, or its key endpoint is
unavailable. Requiring a valid assertion authenticates the caller but does not
authorize that app; handlers must still apply their caller/business policy.
Non-`net/http` integrations can use the lower-level
`FetchTrustedKeys` and `Verify` functions.

Until signed assertions are enabled everywhere a call can come from, an absent
assertion is still possible; never treat it as verified identity.

## Internal-only ingress

Apps on every plan can be hidden from the public edge while remaining reachable
from authenticated same-account service calls. Set the visibility at create
time (`visibility: "internal"`) or update an existing app:

```bash
gregale app APP_ID --visibility internal
gregale app APP_ID --visibility public
```

A Compose project sets this for you: a new app for a service without `ports:`
is created internal, as Docker publishes nothing for it (ADR-20261022131852).
The plan shows `visibility=internal`. The choice is made once, when the app is
created; later syncs keep whatever visibility the app has, so publish such a
service with `--visibility public`. PR previews inherit their app's visibility.

Internal apps do not receive a public platform-subdomain or verified custom
domain route. Service discovery continues to resolve them through
`APP_SLUG.svc.gregale:10081`, where the service proxy enforces caller identity
and same-account authorization. Visibility changes are audited and invalidate
the gateway route cache.

Internal services scale to zero like any other app. A service call to a parked
target is held at the node-local proxy while the snapshot is restored, then
forwarded — the same wake-blocking contract the public edge offers (ADR-196).
The restore is coalesced with any concurrent public request for that app, so a
burst of internal callers costs one restore rather than one per caller. Set
client timeouts above the platform wake budget plus your own handler time, and
note that a fully cold chain (`public-api` → `auth` → `billing`) pays each
restore in sequence. `min_instances` remains available to trade resident RAM
for first-call latency, but it is no longer required for an internal
dependency to be reachable.

When a wake cannot produce a replica, the proxy answers `503`. A saturated
wake queue carries `Retry-After`; a target at its plan concurrency ceiling
reports `service has no healthy replicas`. Internal wakes appear in the wake
timeline with trigger `service.mesh`, distinct from public `gateway` traffic.

Internal calls honour the target's wire protocol (ADR-197). An app configured
`app_protocol: grpc` or `http2` is reached over the H2C guest bridge, and the
node-local listener accepts H2C prior knowledge, so a workload can use an
ordinary gRPC client against `http://APP_SLUG.svc.gregale:10081`. Response
trailers — including `grpc-status` — are preserved across the hop.
`Connection: Upgrade` requests (WebSocket and friends) take the verbatim-bytes
bridge and are neither buffered nor retried; they require the target app to
have WebSockets enabled and return `501` otherwise. Non-HTTP protocols use
private TCP addresses, described below.

### Managed binding application adoption

Bindings inventory reports `application_adoption` for PostgreSQL and object
storage. It maps the binding's managed secret versions to authorized resident
main/sidecar workloads and shows separate guest reload and application counts
(`current`, `failed`, `stale`, `unknown`). Counts represent workload/secret
pairs. PostgreSQL expects its connection secret; object storage expects the
endpoint, region, bucket, access key, secret key and addressing style metadata.
Workload grants determine which of those secrets require acknowledgement.
Task guests, jobs, mirrors and stopped/parked instances are excluded. JSON
includes each target's deployment, runtime, workload, key and observed versions.
It contains no credential values, hashes or private binding owner IDs.

To require application acknowledgements during a rollout:

```sh
gregale app binding check public-api --deployment v12 --require-application-ack --json
gregale release traffic promote --app public-api --deployment v12 --if-serving v11 --require-bindings --require-application-ack
```

Strict mode requires current version-bound acknowledgements from every
eligible authorized resident workload of each PostgreSQL/object-storage
binding in the candidate scope, including a resident candidate target for
each binding. Enable the workload's existing secret reload/application
acknowledgement contract, start the candidate and wait for it to apply and
acknowledge the current secrets. Missing, stale, failed, unsupported and
invalid observations block. Current-version reload failures also block.
Main workloads and long-running sidecars can independently opt into reload
and application acknowledgements in the same deployment. Each workload is
evaluated against its own secret grants and image reload opt-in. Main receives
no implicit secrets when companions are declared; explicitly grant its binding
secrets through `env_secrets`. Main acknowledgements use the standard endpoint;
sidecar acknowledgements include their stamped `?workload=<name>` identity.
An older valid guest projection remains separate from a newer application
receipt. Application receipts have no probe-age expiry; a changed secret
version makes an old acknowledgement stale. The existing probe, rotation and
instance timestamp checks still apply. `--allow-unsupported` cannot waive
application adoption. The default policy remains unchanged.

Strict promotion uses
`POST /v1/deployments/{id}/promote-with-application-ack`, which always enforces
this policy even if its request field is omitted or false. CLI and Go clients
select it automatically for `require_application_ack=true`; Node/Python
clients expose `promoteDeploymentWithApplicationAck` /
`promote_deployment_with_application_ack`. Older servers return 404 before
changing traffic; do not fall back to ordinary promotion. Receipts echo
`require_application_ack`, and changes to secret versions, workload grants,
reload support or acknowledgements invalidate the check at the traffic write.

An optional adoption read failure produces incomplete/unknown adoption and a
sanitized warning; it blocks strict checks. Application acknowledgements are
self-attestations and do not independently prove readiness, ongoing health or
actual credential use. See [ADR-502](adr/502-binding-application-adoption.md).
### Stored binding release enforcement

Enable verification enforcement for an app's deployment scope after establishing
its stable serving deployment:

```sh
gregale app binding release-policy get public-api --scope production
gregale app binding release-policy set public-api --scope production \
  --require-verification --max-age 10m --expected-revision 0
```

Add `--require-application-ack` to require current managed-secret application
acknowledgements. Use the current revision from `get` for subsequent updates.
Unconfigured scopes remain off. Enabling the policy does not change serving
traffic or run probes. Verification age must be a whole number of seconds from
1s through 24h.

Admit new candidates with explicit zero traffic, verify the exact candidate,
then promote it or advance its existing canary. The server enforces the saved
policy on ordinary traffic PATCH, promotion and manual/worker canary advances,
including any deployment gaining redistributed traffic. Request flags cannot
relax the saved policy. Missing, stale or changed evidence returns a structured
conflict with binding blockers. Smoke tests remain diagnostic and do not count
as binding verification.

To admit a new canary under enforcement, use a custom ladder whose first stage
is 0%. The built-in presets start with positive traffic and are blocked at
admission. The zero stage keeps the stable deployment serving while you verify
the candidate; subsequent advances apply the stored binding policy.

To abort an active canary while enforcement stays enabled, first verify the
exact retained predecessor, then pin both deployments:

```sh
gregale app binding verify public-api --deployment v41 --all
gregale rollouts recover public-api --action abort --deployment v42 \
  --expected-predecessor v41 --reason 'Restore verified predecessor'
```

The predecessor must remain live and serving in the same deployment scope.
Recovery checks its current bindings, policy revision and evidence expiry in
the traffic transaction, restores it to 100%, and aborts the selected canary.
The JSON receipt includes both IDs, the committed restored percentage, binding
check reports and the audit ID. Automatic canary aborts from the circuit breaker,
alert demotion, stuck-rollout recovery, critical route health and an expired
progression-worker lease also check the restored recipient. Health and lease
recovery retain their existing safety conditions. Missing or changed evidence
leaves traffic unchanged; recovery does not run verification tasks implicitly.
Binding refusals can be retried with the same idempotency key after supplying
fresh evidence, while successful recovery responses are replayed.

Positive initial canary admission, legacy canary recovery,
unchecked historical rollback and project release graph switches currently fail closed in
enforced scopes when they would increase protected traffic. Graph-wide checked
activation is not yet supported. During an incident, explicitly disable the
policy with a reason before using an unguarded recovery path:

```sh
gregale app binding release-policy set public-api --scope production \
  --mode off --expected-revision 1 --reason 'Restore stable deployment during incident'
```

Recover, then re-enable with the new revision. Every policy update has durable
history; disabling does not require binding/provider availability. Policy
changes and evidence are rechecked inside the traffic transaction, so a
concurrent update or expired observation cannot carry an old approval into a
traffic increase.


### Binding-checked service handoffs

A service rollout under enforcement keeps its predecessor serving while Gregale
checks the exact ready candidate. Missing evidence appears as bounded blockers
in `service_rollout_handoff.bindings_check`; supplying current verification lets
the worker retry without changing the policy. The routing transaction rechecks
policy, evidence and ready service capacity before publishing weights. Gateway
acknowledgement and request drain still determine handoff completion.

An exact abort uses the same pinned pair as canary recovery, including a retained
live predecessor at zero weight after service cutover. Verify that predecessor,
then request the abort and wait for the selected rollout:

```sh
gregale app binding verify public-api --deployment v41 --all
gregale rollouts recover public-api --action abort --deployment v42 \
  --expected-predecessor v41 --reason 'Restore verified predecessor'
gregale rollouts status public-api --deployment v42 --wait --timeout 10m
```

Service recovery returns 202 and `service_recovery` with the request UUID and
both IDs. This confirms acceptance only. `rollouts status` shows binding checks,
routing, missing gateway acknowledgements and drain progress. With `--wait`, a
service abort succeeds only at `rollout_state=aborted` and handoff phase
`complete`; promotion waits for its corresponding complete handoff. Status and
wait use GETs only, never run verification, and preserve the last status on a
timeout. Add `--json` for the exact deployment response. A restart of the
Gregale API resumes the durable request; a missing pinned predecessor cannot be replaced silently.
See [ADR-600](adr/600-binding-checked-service-handoffs.md).

Completed deployments can be restored through an exact binding-checked rollback:

```sh
gregale rollback api --to v41 --expected-current v42 --reason "restore previous release" --wait
```

The accepted operation retains both deployments through readiness and binding
checks. The current release keeps serving while the target prepares at zero
traffic. If bindings are blocked, verify the exact target with the existing
`gregale app binding verify-all api --deployment v41` command, then inspect or resume
waiting for the accepted operation:

```sh
gregale rollback status api --operation OPERATION_UUID --wait
```

Both waits use read-only operation polling. A changed current release fails the
operation rather than selecting another deployment. Service completion includes
gateway acknowledgement and draining. A timeout keeps the last status and binding
blockers visible; an interrupt exits 130. Add `--json` for the operation receipt.
See [ADR-601](adr/601-binding-checked-historical-rollback.md) for transaction boundaries.

## Private TCP between services

Same-account services can also talk over plain TCP, which covers protocols
such as PostgreSQL, Redis, MQTT, AMQP and custom binary protocols. Dial the
service name on its own port:

```text
redis://cache.svc.gregale:6379
postgres://db.svc.gregale:5432
```

`cache.svc.gregale` resolves to a stable private address for the `cache` app,
for example `198.19.0.7`. The address belongs to the app, not to a machine,
so it stays the same across restarts, scale-to-zero and moves between
nodes. It is meaningful only inside your account; another account's
workloads cannot use it to reach your services. HTTP calls to
`http://cache.svc.gregale:10081` keep working on the same name.

A service exposes these TCP ports:

- every TCP port it declares in `ports`, including internal ones (see below);
- the port it serves on (its main listener).

Image `EXPOSE` entries other than the serving port are not exposed
automatically: declare them. Ports 10080, 10081 and 443 on a service address
always belong to the HTTP service mesh.

Declare listeners that should never be public with compose `expose:`:

```yaml
services:
  api:
    build: ./api
    depends_on: [cache]
  cache:
    build: ./cache
    expose: ["6379"]
```

`expose:` entries become `internal` listeners. Same-account services reach
them at the service address, but they never get a public
`--port-<name>` hostname or a public raw TCP listener. Only TCP entries are
supported; UDP and port ranges are skipped with a warning. Through the app
API, set `"internal": true` on an entry in `ports`.

Each declared dependency also receives `GREGALE_SERVICE_<NAME>_HOST`, the bare
host for non-HTTP clients:

```bash
redis-cli -h "$GREGALE_SERVICE_CACHE_HOST" -p 6379 PING
```

The same caller policy as HTTP service calls applies when the connection
opens: same account, the caller's `depends_on` (under the `declared` policy,
and for targets outside the caller's project under `project`), the target's
allowed callers, and preview scoping. A target that grants a
caller only method/path scopes (`x-gregale-allow-call-scopes`) refuses that
caller's raw TCP connections, because those scopes cannot be enforced on raw
bytes.

A parked service is woken when a connection arrives. The connection is held
for up to 30 seconds, then forwarded. While bytes keep flowing, the target
stays running. A connection that is idle for longer than the target's idle
timeout is closed, so an idle connection pool does not keep a service awake
longer than idle HTTP traffic would. Configure your client pool to reconnect.
Concurrent private TCP connections are capped per account on each node, by
plan.

A connection that is refused (unknown service, undeclared port, denied
caller, or a target that cannot be woken) is reset with no data, since raw
TCP carries no error message. Raw TCP connections carry no caller-assertion
header; authenticate inside your protocol if the target must know the
caller.

Private TCP addresses are a node-level rollout. Until your node has it
enabled, `*.svc.gregale` keeps answering with the HTTP-only address, and HTTP
service calls work as before.

### Qualify and activate a project release graph

For projects using enforced binding release policies, prepare a fixed graph,
verify its dependencies, then activate it with an atomic check. Every project
workload must appear in the selection, with a materialized live deployment in
the selected environment and enough revision retention for the release TTL.
Every member scope needs an enforced policy. Preparation and verification keep
the current routes.

Save a selection such as this as `release-selection.json`, using real deployment
UUIDs for each workload:

```json
{
  "ttl_seconds": 1800,
  "deployments": {
    "shop-api": "11111111-1111-4111-8111-111111111111",
    "shop-billing": "22222222-2222-4222-8222-222222222222"
  }
}
```

```bash
gregale environment release-sets prepare shop production \
  --file release-selection.json > release-manifest.json
gregale environment release-sets verify shop production \
  --file release-manifest.json --dry-run --json
gregale environment release-sets verify shop production \
  --file release-manifest.json --json
gregale environment release-sets activate shop production \
  --file release-manifest.json --json
```

If the preview plans storage uploads and cleanup, add
`--object-storage-write` to the verification command after reviewing the plan.
The verifier reuses fresh compatible evidence and waits for compatible pending
probes and required queue readiness. It previews every member before any task.
Internal service edges use HTTPS HEAD probes pinned to the graph's exact target
deployments. These probes require a healthy registered endpoint and never wake
the target or call its handler. External services retain ordinary probe coverage.

The manifest fixes the candidate deployments, current routing, service targets
and policy revisions. Activation rechecks the complete graph, evidence age,
policy, artifacts and service replica readiness in one transaction. A concurrent
release, policy/config change, missing member, stale proof or loss of readiness
blocks the switch. Prepare and qualify again when the intent changes. The first
release also compares the existing weighted routing and requires it to be
unambiguous. Qualification alone does not reserve capacity or guarantee future
readiness.

The API routes are `POST /v1/projects/{slug}/environments/{environment}/release-sets/prepare`
and `POST /v1/projects/{slug}/environments/{environment}/release-sets/activate`.
Exact service evidence can be inspected through the binding inventory's
`service_targets` JSON query parameter together with `deployment_id`.
Ordinary inventory remains separate from evidence for proposed targets.
Generic graph publication and deactivation stay blocked under enforcement;
Checked fallback deactivation is described below.


### Recover a retained project release graph

Select an inactive, unexpired release from the release-set inventory. Save its
UUID and the desired release retention as `rollback-selection.json`:

```json
{"release_id":"33333333-3333-4333-8333-333333333333","ttl_seconds":1800}
```

```bash
gregale environment release-sets prepare-rollback shop production \
  --file rollback-selection.json > rollback-manifest.json
gregale environment release-sets verify shop production \
  --file rollback-manifest.json --dry-run --json
gregale environment release-sets verify shop production \
  --file rollback-manifest.json --json
gregale environment release-sets rollback shop production \
  --file rollback-manifest.json --json
```

Add `--object-storage-write` when the reviewed verification plan requires it.
The graph must match current project membership and every member must satisfy
current enforced policy, artifact eligibility and service readiness. Verification
uses exact historical service targets. Preparation and verification do not switch
routes or wake targets. Expired graph history cannot be recovered even when its
deployments remain live or pinned independently. The historical source and the
expected current release are rechecked atomically, with expiry rechecked at
PostgreSQL commit. A failure leaves the current graph intact.

Recovery creates a new release. Release inventory exposes
`rollback_source_release_id` and `rollback_previous_release_id` as its durable
recovery record. The API uses `POST .../release-sets/prepare-rollback` and
`POST .../release-sets/rollback`; use an `Idempotency-Key` for API mutation retries.
A stale unkeyed retry fails rather than publishing a second recovery. This does
not undo application data migrations or lift GitOps ownership restrictions.

### Deactivate a project graph with checked fallback routing

Observe the active graph and its weighted fallbacks, qualify them, then explicitly
deactivate. Every current workload must have exactly one eligible materialized
live 100% fallback in the environment and an enforced binding policy. Missing
fallbacks and traffic splits block preparation.

```bash
gregale environment release-sets prepare-deactivation shop production \
  --json > deactivation-manifest.json
gregale environment release-sets verify shop production \
  --file deactivation-manifest.json --dry-run --json
gregale environment release-sets verify shop production \
  --file deactivation-manifest.json --json
gregale environment release-sets deactivate shop production \
  --file deactivation-manifest.json --json
```

Supply `--object-storage-write` when the reviewed verification plan needs a
storage canary. Preparation and verification keep current routing. Internal
service probes qualify the exact fallback deployments; they do not wake targets.
Deactivation rechecks the current graph, complete fallback routing, current
policies, fresh evidence, artifacts and service readiness atomically. Changed
routing, stale proof or missing readiness keeps the active graph intact.

The manifest has `deactivate: true` and `ttl_seconds: 0`. No new deployment
retention setting is required for already-serving fallbacks. Retirement preserves
the graph's existing TTL and retained member access for requests carrying its ID.
New requests use the qualified weighted routes. The retired release's
`deactivation` field records the receipt ID, release ID, completion time and every
fallback app/deployment pair. The API routes are `POST .../release-sets/prepare-deactivation`
and `POST .../release-sets/deactivate`; use an `Idempotency-Key` for mutation retries.
The same key replays the completed receipt; stale unkeyed requests fail. Existing
unchecked deactivation and GitOps-managed workloads remain blocked under enforcement.

### Stage candidate services before graph qualification

A prepared forward or rollback graph may select a dark service deployment with
no ready replicas. Service probes do not wake targets. Use a temporary staging
lease to bring those exact service deployments up through the normal
admission and readiness gates, while keeping current weighted and graph routes.
Selected deployments must already be live and materialized; use the existing
dark-deployment preparation workflow first. GitOps-managed workloads, active
service rollouts, mirror recipients and deactivation manifests are excluded.

```bash
gregale environment release-sets stage shop production \
  --file manifest.json --ttl-seconds 600 --json > stage.json
stage_id=$(jq -r .id stage.json)
gregale environment release-sets stage-status shop production \
  --stage "$stage_id" --json
# Once status is ready, qualify exact targets and explicitly publish the graph.
gregale environment release-sets verify shop production --file manifest.json
gregale environment release-sets activate shop production --file manifest.json
# Use rollback instead of activate for a prepared rollback manifest.
```

`stage-status` reports `waiting`, `ready`, `blocked`, `expired` or `cancelled`,
with per-service ready/desired counts and a reason for blockers. Capacity, startup
or dependency failures and changed manifests are observable. Readiness is an
observation, not binding evidence: activation still independently checks current
policies, exact service evidence, membership, artifacts and replica readiness.
Staging gives function and worker members no residency lease.

The capacity is billable and uses normal concurrency and residency limits.
Staging preserves serving allocations and provides no rollout overlap allowance.
A full plan can block the candidate until spare capacity is available. The lease
lasts 1–1800 seconds; repeating the same manifest reuses the original stage ID
and expiry. Another manifest conflicts while that scope has an unexpired,
uncancelled stage. Cancel before staging a different selection:

```bash
gregale environment release-sets cancel-stage shop production \
  --stage "$stage_id" --json
```

Cancellation and expiry remove only the temporary allocation. An in-progress
boot may complete and is cleaned up by normal reconciliation. Current traffic
and active graph allocations remain authoritative, so cancellation does not tear
down a service serving the active graph. Active graph members retain their desired
service allocation after activation invalidates the staging manifest.

The API routes are `POST .../release-sets/staging` with
`{ "manifest": <prepared manifest>, "ttl_seconds": 600 }`, and
`GET` / `DELETE .../release-sets/staging/{stage}`. They enforce existing account,
project and environment scope, MFA and deploy/read permissions. Mutation retries
support `Idempotency-Key`. This scheduler lifecycle change requires dedicated
native KVM `test-metal` and `leakcheck` acceptance before deployment; see
[ADR-1351](adr/1351-checked-project-service-staging.md).

### Push queue binding completion canaries

A healthy poll proves consumer readiness. Select `handler_completion` when a release must also prove that a safe message reached the selected deployment and its handler completed successfully.

The deployment declares supported logical binding names in its `override_env`:

```json
{"GREGALE_QUEUE_CANARY_BINDINGS":"[\"orders\"]"}
```

The consumer must recognize `{"gregale_queue_canary":"v1","canary_id":"<unique UUID>","binding":"orders"}` and handle it safely. Return success only after the safe test handler finishes. Successful HTTP handling does not certify application-specific business effects. Pull consumers are unsupported. Shared legacy bindings support canaries in production/default only; other environments require an explicitly scoped queue binding.

```sh
gregale app binding release-policy set my-api --scope production --require-verification --queue-require orders=handler_completion --expected-revision 0
gregale app binding verify my-api --deployment v12 --release-policy --dry-run --json
gregale app binding verify my-api --deployment v12 --release-policy --queue-publish --json
gregale app binding verify my-api --deployment v12 --queue orders --queue-publish --json
```

Checked graph verification also accepts `--queue-publish`. It reviews every member and publication consent requirement before admitting messages. Dry runs never publish; compatible pending canaries are observed and fresh successful evidence is reused.

The API exposes `POST /v1/apps/{slug}/bindings/queue-canaries` with `deployment_id`, `binding`, `ttl_seconds` (1–600) and `publish_consent: true`; `GET` and `DELETE /v1/apps/{slug}/bindings/queue-canaries/{canary}` observe and cancel. `published` and `dispatching` are not passes. Confirmed success reports delivery and completion timestamps. Raw handler output and credentials are not returned. Cancellation revokes evidence even when an in-flight handler is finishing; CLI interruption or timeout requests cancellation.

Canaries use normal queue/runtime limits, retained revision pins and one execution attempt. They do not change release routing. Evidence expires at the original deadline or sooner under the policy's maximum age. Consumer or deployment changes invalidate it. Replay and stale acknowledgements cannot qualify a release.

The shipped `queue-worker` starter handles the reserved canary before business
jobs and rejects malformed or undeclared probes. Opt-in remains an exact
deployment override plus explicit publication consent; its README includes the
API setup and durable verification commands. The
[native queue acceptance journey](ops/queue-bindings-native-acceptance.md)
exercises this starter, real delivery/restore, scheduler recovery and release
fences. Supported native execution remains required for qualification.

Ordinary durable dispatch does not perform a separate canary lookup. The existing
ledger ownership read also identifies registered canaries, and only those
invocations undergo full completion-canary validation. Caller-controlled source,
payload and headers cannot skip these checks; cancelled and expired canaries
remain registered until ordinary retention removes them. See
[ADR-1354](adr/1354-ledger-backed-canary-dispatch-authority.md).

### Parallel binding qualification

`gregale app binding verify APP --deployment ID --release-policy --concurrency 2`
runs independent required probes concurrently. The default is 2; `--concurrency`
accepts 1–8 and applies only to release-policy verification. Use 1 for serial
execution when provider or application capacity requires it. Server admission
limits still apply.

`gregale environment release-sets verify PROJECT ENV --file release.json
--concurrency 2` shares the same limit across all graph members. Review with
`--dry-run`; planned object writes still require `--object-storage-write`, and
planned queue publication still requires `--queue-publish`. No probe starts until
every member has passed the plan and consent review.

The CLI preserves reviewed deployment, configuration, policy and service target
snapshots. Results stay in plan order. Failed or interrupted runs stop new
admissions and wait for cleanup of work they created; preexisting queue canaries
remain available to their original owners. After all members finish, graph
verification rechecks current evidence against the original snapshots. Evidence
expiry and the atomic checks at activation remain unchanged. See
[ADR-1353](adr/1353-bounded-parallel-binding-verification.md).

### Durable single-app binding verification

Use a durable receipt when a CI job or CLI session may disconnect:

```sh
gregale app binding verify my-api --deployment v12 --release-policy --durable \
  --object-storage-write --queue-publish --concurrency 2 --json
```

Only include `--object-storage-write` or `--queue-publish` when you consent to
those planned effects. Review with `--dry-run` first; a dry run creates neither
a receipt nor a resource. `--durable` requires `--release-policy` and a wait
budget of at least one second. The lifetime is capped at ten minutes. To make
creation recovery deterministic in CI, supply your own UUID with `--run-id`.
The CLI prints the run ID before requesting creation and includes `run_id` in
its verification report.

```sh
gregale app binding verification-runs status my-api RUN_ID --json
gregale app binding verification-runs resume my-api RUN_ID --concurrency 2 --json
gregale app binding verification-runs cancel my-api RUN_ID --json
```

Status shows the original plan, resource IDs, ownership and current progress.
Receipt details require the same domain catalog access as their binding inventory.
Resume keeps the original deployment, policy, configuration, binding metadata,
consent and deadlines. It observes compatible pending work and fresh results;
failed, missing, cancelled, changed or expired work requires a newly reviewed run.
It does not silently publish another message or replace a task. A shorter resume
wait budget is allowed and never renews the server deadline.

Each new task/message and its receipt entry commit together. If the client crashes
or loses that HTTP response, the next admission request returns the existing
resource. Transient transport errors preserve owned work for resume. An explicit
cancel, interrupt or exhausted CLI wait budget blocks new admissions and requests
cleanup of unfinished owned work. Preexisting work remains owned by its original
creator. In-flight handling may still finish; cancellation is cooperative.

Admission and publication remain insufficient for qualification. The server only
completes a run after successful owned work and fresh policy preflight. Current
proof can subsequently expire or change; activation still performs its atomic
policy and freshness checks. Runs are limited to 128 steps and eight unfinished
unexpired receipts per app, use normal queue/runtime capacity, and are pruned after
one day of expiry on the next creation for that app. This workflow currently covers
one app; graph verification retains its existing prepared-manifest workflow. See
[ADR-1355](adr/1355-durable-single-app-binding-verification-runs.md).

### Durable project graph verification

Persist the prepared graph and every member's review before admitting probes:

```sh
gregale environment release-sets verify my-project production \
  --file prepared.json --durable --run-id RUN_UUID \
  --queue-publish --object-storage-write --concurrency 2 --json
```

Only include effect consent flags for effects you reviewed. `--dry-run` previews
all members without creating receipts, tasks or messages. Durable verification
also accepts prepared rollback and deactivation manifests. It requires a wait
budget of at least one second, capped at ten minutes. The parent and every member
receipt commit together and share that fixed deadline. The CLI prints the parent
ID before creation, so a lost creation response is recoverable with the same UUID.

```sh
gregale environment release-sets verification-runs status my-project production RUN_UUID --json
gregale environment release-sets verification-runs resume my-project production RUN_UUID --concurrency 2 --json
gregale environment release-sets verification-runs cancel my-project production RUN_UUID --json
```

Resume retains membership, routing expectations, policy revisions, exact service
recipients, effect consent and resource ownership. Changed membership, private
configuration, failed work or expired evidence requires a new review. Retries use
the recorded member and resource IDs. Explicit cancel, interrupt or exhaustion of
the root CLI wait budget fences the graph before cooperative cleanup of unfinished
owned work. Transient transport errors preserve work for recovery.

Completion freshly checks all members after their probes finish. Graph passed
status does not authorize activation: activation still performs its atomic graph
and binding checks. Graph receipts use the existing 100-member and per-member
admission bounds, at most eight active graph runs per project/environment, and
normal runtime/queue capacity. Receipt requests are limited to 1 MiB. Receipts
older than one day after expiry are pruned on the next creation for the project.
GitOps candidate holds retain their existing checked workflow. See
[ADR-1356](adr/1356-durable-project-binding-verification-runs.md).

### Review a binding verification plan

App and project graph verification reviews are side-effect-free reads:

- `POST /v1/apps/{slug}/bindings/verification-runs/review` accepts an optional deployment ID and exact service targets. An omitted deployment selects the current live deployment and returns its exact ID.
- `POST /v1/projects/{slug}/environments/{environment}/release-sets/verification-runs/review` accepts a prepared release, rollback or deactivation manifest and the intended effect consent flags.

Responses contain public inventory, policy, the plan, blockers, required consent and opaque review digests. `ready` means the plan is available; it does not mean probes or release checks passed. Reviews never run probes, publish messages or create receipts. Blocked reviews have no usable digest.

Forward the returned deployment, targets and digest into app receipt creation. For graph creation, forward the manifest, member `review_digests`, aggregate `review_digest` and the same consent flags. Creation rechecks current state and rejects a stale or altered review. CLI `--dry-run` uses these endpoints; durable verification reviews on the server before creating its receipt.

### Run an existing verification receipt from an SDK

Go provides `Client.RunBindingVerification` and `Client.RunProjectBindingVerification`. Node provides `runBindingVerification` and `runProjectBindingVerification`. Python provides `run_binding_verification` and `run_project_binding_verification`, plus async `arun_` variants.

Save the caller-selected receipt UUID before creation. After forwarding the server review and explicit consent to creation, pass that UUID to the runner. It resumes frozen steps, observes task/canary status and asks the server for final qualification. Success requires a confirmed passed completion; graph completion also requires every member to pass. Verification never activates routes.

The default concurrency is two unfinished resources, configurable from one to eight across the entire graph. Existing work counts toward that budget. A runner never replaces reused work or renews expiry. Multiple runner invocations do not share a distributed client lease; server admission and capacity limits still apply.

SDK transport failures, caller stops and progress callback failures preserve server work and identify the same run for resumption. Use the existing cancel API when cancellation is intended. Go context cancellation, Node abort signals and Python stop events stop observation; asyncio task cancellation carries `run_id`. Synchronous Python stops are checked between requests, whose configured HTTP timeouts are bounded by the remaining receipt lifetime. The CLI retains its interrupt/deadline cleanup policy. Durable CLI JSON reports include sanitized `resource_id`, `resource_kind` and status without reconstructing guest stage reports.

### Explain blocked verification receipts

Use `gregale app binding verification-runs diagnostics APP RUN_ID --json` or `gregale environment release-sets verification-runs diagnostics PROJECT ENVIRONMENT RUN_ID --json` to inspect blockers and recovery suggestions. The corresponding GET endpoints end in `/verification-runs/{run}/diagnostics` on the app binding or project environment release-set surface.

Responses preserve the original run ID and expiry and include `complete`, optional `check_passed`, `blockers`, `changes` and `recovery`. `complete` means applicable diagnostic reads succeeded; `check_passed` describes current public policy/evidence. Neither grants admission, confirms receipt completion or authorizes traffic changes. A previously passed or expired receipt can have current blockers.

Differences contain only whitelisted public values such as policy revisions, configuration timestamps, credential generations and selected service target IDs. Credentials, provider origins, private commands/pins, raw errors and guest stdout are excluded. Incomplete reads produce retry guidance rather than false binding-removal differences. Access to every reviewed binding domain is required, including when the original deployment is unavailable.

Recovery actions distinguish continuing the original run, waiting for application acknowledgment/readiness, retrying diagnostics, explicitly cancelling unfinished owned work, and reviewing a new receipt. Changed original authority suppresses resume guidance. Diagnostics perform no execution or cleanup and never renew expiry. Get fresh diagnostics or invoke the existing resume/cancel APIs deliberately; those operations independently enforce permissions and current authority.

### Shared verification admission budgets

Receipt creation accepts `max_concurrency` (one through eight, default two). The CLI freezes `--concurrency` into a newly created durable receipt. Every driver of that receipt shares the server budget, and project graph members share one root budget. Resuming a receipt cannot change it; the runner's local concurrency may further restrict admission attempts. Independent receipts retain separate budgets, and normal scheduler, queue and account capacity still apply.

At capacity, step admission returns HTTP 429 with code `binding_verification_budget_full`, `limit` and `observed`. Current SDK runners wait, reread the same receipt, and retry within its original deadline. Other capacity errors still stop observation with the original receipt ID. An admission replay returns its original resource even when the budget is full.

Diagnostics include `budget.run_id`, `max_concurrency`, `active_resources`, `waiting_steps` and `full`. Counts deduplicate resource identities and include borrowed unfinished work. Cancellation requests or expired canary evidence do not free a slot while the underlying task/invocation remains unfinished. Terminal or missing resources free capacity; they do not count as successful verification.

### Automatic recovery explanations

Durable verification failures now include best-effort diagnostics in CLI JSON output under `diagnostics`. Project failures include root diagnostics and available member diagnostics in `reports`. Terminal output shows a short blocker/recovery summary. The original failure, exit code and receipt ID remain authoritative for the client outcome; successful diagnostic reads never make verification pass.

Go recovery errors expose `Diagnostics` or `ProjectDiagnostics`. Node and Python recovery errors expose a typed `diagnostics` value. Lookups are read-only and bounded to two seconds, the caller deadline and a confirmed original receipt expiry. Cancelled calls and local callback failures skip enrichment; unavailable, forbidden or mismatched responses leave the original error intact. `SkipDiagnostics`, `skipDiagnostics` or `skip_diagnostics` disables enrichment in the corresponding SDK. Manual `verification-runs diagnostics` remains available. Suggested recovery is never executed automatically.

### Create and reconcile reviewed verification receipts

SDK create-from-review helpers accept an explicit ready review, caller-supplied canonical UUID, lifetime, concurrency and effect consent. Persist the UUID before calling creation. Helpers validate the review and returned selection without computing digests. Server freshness, authorization and idempotency checks remain authoritative.

A creation error preserves the original cause and UUID plus whether creation was invoked; this does not prove acceptance. Use the separate reconciliation helper to GET that same ID once and validate the receipt against the same review/options. A failed or missing read does not prove noncreation. Reconciliation permits terminal or expired matching receipts and performs no resume, admission, renewal or cancellation. Run a confirmed resumable receipt explicitly with the existing runner.

CLI creation failures with `--json` include `run_id`, app or project/environment, `passed=false`, `status=creation_failed`, `create_attempted`, `error` and recovery guidance. Inspect the same ID with `verification-runs status` after uncertainty. See [ADR-1362](adr/1362-reviewed-verification-creation-helpers.md).

### Retry transient verification status reads

Durable runners retry temporary polling GET failures and HTTP 502/503/504 up to three additional times per read. Backoff uses equal jitter with exponential delay from 250 ms, capped at two seconds, and obeys the original receipt expiry and caller cancellation/deadline. Lifecycle writes, diagnostics, permanent HTTP failures and malformed or mismatched successful responses retain their existing recovery behavior. Exhaustion returns the existing recovery error and diagnostic enrichment.

Set `--status-read-retries 0` on durable CLI verify or receipt resume to disable; values through eight are supported. Go options use `StatusReadRetries` (nil defaults to three, pointer to zero disables), Node uses `statusReadRetries`, and Python uses `status_read_retries`. Progress callbacks receive root `retrying` events with `retry_attempt` and `retry_delay_ms`. No new receipt, renewed expiry or duplicate admission is introduced. See [ADR-1363](adr/1363-bounded-verification-status-read-retries.md).

### Discover retained verification receipts

Use `gregale app binding verification-runs list APP --json` or `gregale environment release-sets verification-runs list PROJECT ENVIRONMENT --json` when you need to recover a receipt ID. Lists are read-only summaries; they do not refresh evidence, admit work, resume or cancel receipts. A listed passed state does not establish current qualification.

Pages default to 20 entries (maximum 100), newest first by creation time and UUID. Use `--limit`, `--cursor`, `--deployment UUID` and `--status pending|passed|cancelled|expired`. App lists additionally accept `--scope` and `--include-members`; by default they omit graph members. Project lists show root receipts and match deployment filters against reviewed members. Included app members expose their original `graph_run_id`.

Reuse filters with `next_cursor`; `observed_at` freezes expiry evaluation across pages. Newer creations do not shift existing page boundaries. Concurrent state changes and retention pruning can remove matches, so pages are not a historical snapshot or permanent audit log. Existing receipt cleanup is unchanged. Inspect the selected UUID with status/diagnostics before choosing a recovery action. See [ADR-1364](adr/1364-verification-run-discovery.md).

### Waiting for final binding qualification

Durable app and graph runners now keep observing the original receipt when completion returns HTTP 202 with recognized `qualification` conditions. Progress callbacks receive `waiting_for_qualification` and public condition codes/selectors. This covers runtime refresh, consumer readiness and valid application acknowledgment observations that are still settling. Failed or malformed evidence, unknown conditions and review drift still stop the run.

Waiting uses the existing polling interval, caller stop controls and original expiry. It admits no replacement tasks or queue messages and never renews a receipt. CLI verify/resume inherits the shared runner behavior and existing timeout cleanup policy. HTTP 200 with passed status still requires the authoritative final preflight. See [ADR-1365](adr/1365-verification-qualification-waiting.md).

### Recovering lost admission and completion responses

Durable runners reconcile eligible transport failures and HTTP 502/503/504 by reading the original receipt. Admission proceeds only when that read proves the exact reviewed step owns a valid resource. Completion succeeds only when fresh status confirms the root and all graph members passed with finished resources. Pending completion, missing ownership, failed resources or review drift stop with the original receipt ID and error cause retained.

Progress reports `reconciling` with `operation: admission` or `operation: completion`; admission events identify the selected member and step. Reads use `--status-read-retries` and share the original deadline and caller stop controls. The uncertain write is never automatically replayed. Create, initial resume and cancellation keep their existing explicit recovery behavior. See [ADR-1366](adr/1366-verification-write-reconciliation.md).

### Binding CLI progress and stopped-run recovery

Durable app/graph verify and receipt resume accept `--progress auto|plain|ndjson|off`. Auto prints compact stderr updates for human output and disables live updates under `--json`. Explicit progress flags require durable execution and cannot be used with dry-run, status, diagnostics or cancellation. Planned steps without resources are omitted from plain updates; repeated conditions with only a newer observation time are suppressed.

For CI, keep stdout as one final JSON report and stream typed events on stderr:

```sh
gregale app binding verify api --release-policy --durable --json --progress ndjson
gregale environment release-sets verification-runs resume project production RUN_ID --json --progress ndjson
```

`binding_verification_progress` events include root/member IDs, step/resource status, `waiting_for_qualification` conditions, read retries and `reconciling` operations. Graph events also identify project and environment. During execution, cleanup failures use `binding_verification_notice` with code `cleanup_failed`. Pre-execution argument/authentication failures retain the normal CLI error contract.

Stopped app reports and graph result envelopes include `recovery` with the original root ID, last status/operation, `last_qualification` and exact status/diagnostics commands. These conditions are the last observed wait, with its original check time; they do not assert current qualification. Human summaries show the same recovery context. Turning progress off does not discard it. Creation still announces its recovery ID before the request, using a structured `creating` event in NDJSON mode. Progress output is best effort and never changes execution or timeout cleanup. See [ADR-1367](adr/1367-binding-cli-progress.md).

### Recovering an uncertain verification creation

Durable app/graph CLI verification now reconciles eligible creation transport failures and HTTP 502/503/504 by reading the original caller-selected UUID. It continues only when the receipt matches the reviewed selection, manifest, member identities, consent, concurrency, plans and lifetime and remains usable. A missing receipt, expired/cancelled state, failed resource or changed selection stops with the original creation error and recovery ID. Creation is never automatically posted twice, and no replacement UUID or review is generated.

Recovery is capped at five seconds, the original caller deadline/stop controls and the requested TTL measured from the attempt. `--status-read-retries` bounds extra transient GET attempts; zero allows the initial reconciliation GET without retries. Existing progress modes emit `reconciling` with `operation: creation` and `retrying` when needed. `--json --progress ndjson` keeps the final report on stdout and events on stderr. The normal runner then resumes the same confirmed receipt under its original expiry. See [ADR-1368](adr/1368-verification-creation-reconciliation.md).

### Adopt rotated PostgreSQL pools in application code

The Go, Node and synchronous Python SDKs provide driver-independent PostgreSQL
pool bindings with required readiness validation and transaction leases. Applied
secret ACKs follow validation; old pools drain after leased transactions release
their driver connections. These application checks do not replace platform binding
verification or release qualification. See the [runtime guide](postgres-pool-runtime.md)
and [pg.Pool example](../examples/postgres-pool-rotation/README.md).
