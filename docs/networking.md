# Networking

Inspect the effective network contract for an app:

```bash
gregale app APP_ID network show
gregale app APP_ID network doctor
gregale app APP_ID network attach NETWORK_ID --region REGION --cidrs CIDR[,CIDR...]
gregale app APP_ID network detach
```

Gregale-owned private networks are account-scoped API resources. They are not
DigitalOcean VPCs and do not require cloud credentials. Use `GET/POST
/v1/networks` and `GET/DELETE /v1/networks/{id}` with the API or SDK.

Gregale-owned networks can be connected without a provider-specific VPC
operation. `POST /v1/networks/{id}/peerings` with
`{"peer_network_id":"net-..."}` creates account-scoped peering intent and
returns `202` with `status: "pending"`; inspect it with the sibling `GET` or
list endpoint, and remove it with `DELETE`. Both networks must be in the same
region and use non-overlapping IPv4 CIDRs. Pending and error peerings are
fail-closed, so they never imply reachability; the provider-neutral peering
reconciler is the only component allowed to mark one `ready`, and it does so
only after the configured route applier accepts the complete bidirectional
route set. In Gregale-owned fabric mode, schedd applies that set through the
existing vmmd app-netns attachment update, once per ready attachment and live
compute node; each replay replaces the complete effective CIDR set, so stale
peering destinations are withdrawn. A network referenced by a peering cannot
be deleted until that peering is removed.

`GET /v1/networks/{id}/members` provides the account-scoped member inventory:
each stable address reservation includes its owner type, opaque owner ID, and
address. The response also reports allocatable capacity, used addresses, and
remaining addresses; network, gateway, and broadcast addresses are excluded
from capacity. This is an inventory read only and does not probe workloads or
call DigitalOcean.

The fabric slice persists the network definition and reserves stable member
addresses (network+1 is reserved as the gateway; allocation starts at
network+2). When the fabric flag is enabled, schedd first asks every vmmd
hosting the app to reconcile a dedicated, account-scoped host bridge for that
network, then activates app routes. A bridge failure leaves the attachment in
`error` and traffic blocked. Ready Gregale-owned attachments now get a
node-local private veth side-link into the `gpn-*` bridge, with the stable
network member address allocated at attach time. Private ingress is DNATed to
the guest's fixed `10.0.0.2:8080` contract and guest-originated private traffic
is SNATed back to that member address; the existing `br-tenants` veth remains
the public-egress path. Cross-node transport is converged separately by the
regional `gpx-*` VXLAN link described below.
Set
`FAAS_PRIVATE_NETWORK_FABRIC_ENABLED=1` to dark-launch the resource API; app
attachment remains separately gated by
`FAAS_PRIVATE_NETWORK_ENABLED=1` and stays fail-closed until reconciliation.

`network show` combines the app's outbound CIDR allowlist, static egress
address (when available), same-account service discovery, and captured
upstream observations. Upstream hostnames stay redacted; only the safe hash
fragment, port, and probe metadata are shown.

`network doctor` is non-invasive. It reads configuration and existing probe
telemetry but never sends traffic or credentials to an upstream. A fresh probe
means Gregale has observed a successful RTT within the last 15 minutes; it is
not a new connectivity test.

Same-account apps can call one another as
`http://APP_SLUG.svc.gregale:10081`. For external VPC resources, Pro and Scale
customers can record a provider-neutral attachment intent with `network attach`.
The API accepts non-overlapping RFC1918 IPv4 ranges (up to 16 on Pro and 64 on
Scale), returns `pending`, and keeps traffic blocked until a provider connector
reports `ready`. `network doctor` surfaces pending and failed reconciliation;
it never probes the private network itself. `network detach` is idempotent.

The attachment resource is intentionally provider-neutral: `NETWORK_ID` and
`REGION` are stable lowercase identifiers. A node's connector verifies that
the provider attachment is usable; the runtime reconciler then installs the
requested routes and nftables exceptions before changing the row to `ready`.
Route activation is idempotent, and any connector or host-route failure leaves
the row in `error` with traffic blocked. Until a connector is configured, the
API remains an intent surface and every attachment stays `pending`.

Private-network security policy is opt-in per attachment. Include
`allowed_cidrs` in the PUT body to restrict both private egress destinations
and private ingress sources; every range must be contained by the attached
network CIDR. Omitting it (or sending an empty list) preserves the existing
allow-all behavior, while a pending/error attachment remains fail-closed:

```json
{
  "network_id": "prod-vpc",
  "allowed_cidrs": ["10.42.8.0/24"]
}
```

The policy is persisted with the attachment and sent to each vmmd as part of
the same live update as the stable member address. vmmd installs the nft
accept/drop rules before publishing the new cached state; a failed update is
reported as reconciliation error and never widens access.

Networks can also carry a reusable, account-scoped firewall baseline. Set
`allowed_cidrs` and optional `firewall_rules` when creating a network or
replace them with `PUT /v1/networks/{id}/policy`; every rule is contained by
the network CIDR and is applied to every attached workload. A rule's
`direction` is `ingress` or `egress`, `protocol` is `tcp`, `udp`, or `icmp`,
and TCP/UDP rules carry ports such as `443` or `8000-8080`. Rule CIDRs are
sources for ingress and destinations for egress; an omitted list means the
whole network CIDR. An attachment policy may only narrow the CIDR baseline,
never broaden it. Updating the network policy is asynchronous: schedd
replays the effective policy to every live node immediately after the durable
mutation wakeup, with the periodic sweep as a recovery backstop, and nftables
keeps traffic blocked until each update succeeds. Empty rule lists preserve the legacy
CIDR-only behavior; the PUT body replaces both lists, so include a list when
you intend to retain an existing restriction.

Schedd applies a ready attachment once per live compute node and records a
per-node convergence observation in its reconciliation logs. A partial node
failure keeps the attachment in `error` and is retried by the next sweep;
successful nodes are still reported so operators can identify the unhealthy
box without guessing from aggregate status.

The attachment read endpoint also exposes the last durable per-node result in
`attachment.nodes`. Each row separates `fabric_status` from `route_status`,
includes bounded failure detail, and carries `observed_at`. A node can have a
ready bridge while route policy is still failing; this projection is the
operator view of that partial convergence and is replaced on the next replay.

For multi-node Gregale networks, vmmd can add a provider-neutral VXLAN link
over an operator-managed encrypted overlay (Tailscale, WireGuard, or another
routable underlay). This is disabled by default and does not call DigitalOcean:

```toml
[compute_node]
private_network_transport_enabled = true
private_network_transport_interface = "tailscale0"
private_network_transport_peers = ["100.64.0.11", "100.64.0.12"]
overlay_ip = "100.64.0.10"
```

`FAAS_PRIVATE_NETWORK_TRANSPORT_*` environment variables provide the same
overrides. vmmd derives a stable `gpx-*` link and VNI per account/network,
installs static peer FDB entries, and removes the link before deleting the
bridge. Once every active node has registered an overlay address, schedd
authoritatively refreshes each node's regional peer set and vmmd flushes stale
FDB entries before replacing the current set. During a rolling upgrade, an
incomplete roster falls back to the startup-configured peer list; a node join
or drain converges on the next fabric reconciliation. Every node in a region
must use the same encrypted overlay; leave the flag off until the control-plane
mTLS and underlay policy are validated. When the capture-capable host runner is
available, vmmd also verifies the bridge, VXLAN link, bridge membership, and
exact FDB peer set after each reconcile. Any missing or stale peer is reported
as transport drift and schedd keeps the attachment fail-closed until the next
reconcile repairs it; older vmmds without the probe remain compatible during
the rolling upgrade.

Legacy external-network attachments still use the operator-managed connector
and are enabled in schedd with
`FAAS_PRIVATE_NETWORK_ENABLED=1` plus a `FAAS_PRIVATE_NETWORKS` JSON registry,
for example `[{"id":"corp-vpc","region":"fra1","ready":true}]`. This
keeps cloud credentials out of the control plane while the provider adapter is
rolled out; a future connector can replace the registry without changing the
customer-facing attachment contract.

## Public WebSockets

Enable WebSockets on an app with `gregale app APP_SLUG --websocket-enabled`.
An open WebSocket counts as an in-flight request, so its instance stays
running past the idle timeout for as long as the connection is open.

The public edge closes a WebSocket that carries no frames for about 100
seconds. Send a ping or an application heartbeat at least every 60 seconds,
and reconnect when the connection closes. A connection that carries traffic
stays open.

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
`gregale bindings public-api`, alongside database, object-storage, queue,
and outbound integration bindings. New project workloads use the `declared` caller policy: the gateway
returns 403 for calls to services not listed in `depends_on`. Existing apps
retain their persisted policy on reapply. For an intentional same-account
escape hatch, set `x-gregale-service-policy: account` on the caller. The CLI
reports declared service bindings as `enforced`.

`GET /v1/apps/{slug}/bindings` provides the shared binding inventory used by
the CLI and SDKs. It batches app-scoped catalog reads for services, PostgreSQL,
object storage, queues, and outbound integrations. Omit `scope` to inspect all
resource scopes, or use `?scope=staging` / `gregale bindings public-api --scope
staging` to filter database and bucket bindings. App-wide service, queue, and
outbound bindings remain included; the filter does not create or isolate
resources for that environment.

The binding inventory separates configuration from queue consumer state and
liveness. An enabled queue binding is shown as `enabled`; its consumer can
still be `not_configured`, `not_observed`, `degraded`, or `stale`. Service
`declared`/`enforced`, database `ready`, and storage credential `active` describe
their configuration or provisioning state, not verified application
connectivity. JSON `runtime_status` is `unknown` unless a queue consumer has a
scheduler observation; `observed_at` identifies that observation, while
`generated_at` is the inventory collection time. `verification_status` is
`passed`, `failed`, `unknown`, or `stale` for service and PostgreSQL entries.
The optional `verification` contains sanitized stage statuses, the actual task
scope and deployment, completion time, and PostgreSQL credential generation.
Other binding families remain `unknown`. Outbound entries show integration enablement, credential
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
default; use `gregale bindings public-api --require-complete` to return status 1 for
any incomplete inventory, including that preview. `complete: true` means all
inventory and status reads succeeded, not that every dependency is healthy.
The response is a best-effort projection, not an atomic snapshot, and uses
`Cache-Control: no-store`. An unobserved consumer is not a failed read: its
configuration can be known while its runtime status remains `unknown`.
The command only reads metadata; it does not invoke application handlers or
providers. Use `bindings verify` for service, PostgreSQL or object-storage connection probes.

Use `bindings check` to evaluate those recorded observations in CI:

```sh
gregale bindings verify public-api --all
gregale bindings check public-api --scope production --max-verification-age 10m --json
```

The default check makes one inventory GET and returns exit status 0 when its policy
passes, or 1 for blockers, invalid options or failed reads. It does not create
tasks, invoke providers or restart instances. Omit `--deployment` to use the
current manual-task deployment. Use `--deployment ID|vN` to select an exact live,
materialized deployment, including a candidate receiving zero traffic. Revision
handles resolve within the named app. An explicit `--scope` asserts that the
selected deployment uses that scope; omit it to use the deployment's scope.
App-wide service, queue and outbound bindings remain included. PostgreSQL and
object-storage bindings in other scopes are explicitly skipped.

Use `--wait --timeout 5m --poll-interval 1s` to observe already pending probes,
credential refreshes, runtime replacement or application acknowledgements. Waiting
pins the first selected deployment and scope and prints one final report. It
creates no tasks or refreshes. Terminal failures and missing probe evidence stop
immediately; timeout returns the last blocked report and exit status 1. Ctrl-C
returns 130. Failed reads stop the wait with a nonzero result.

For a staged rollout, verify and check the same deployment before promotion:

```sh
gregale bindings verify public-api --deployment v12 --all
gregale bindings check public-api --deployment v12 --max-verification-age 10m --json
gregale traffic promote --app public-api --deployment v12 --if-serving v11 --require-bindings --max-verification-age 10m
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

Human output identifies blockers and remediation. JSON includes `passed`, the
applied policy, scope and deployment, per-binding coverage, runtime counts,
inventory issues and stable blocker codes such as `verification_expired`,
`verification_scope_mismatch`, `runtime_stale`, `rotation_pending` and
`verification_unsupported`. This is a point-in-time policy result from a
best-effort projection. It does not prove application readiness or resident
credential use; object-storage evidence covers bucket-list read access only.

Customer-sealed outbound integrations can opt into verification by declaring a
provider endpoint safe to call:

```sh
gregale bindings probe-policy INTEGRATION_ID --path /health --method GET --expect-status 200
gregale bindings verify public-api --deployment v12 --outbound INTEGRATION_ID
gregale bindings check public-api --deployment v12
gregale traffic promote --app public-api --deployment v12 --require-bindings
```

The method must be GET or HEAD, the canonical path must fit the integration's
route policy, and the expected status must be 2xx. This declaration sends no
provider requests. `bindings verify --all` includes configured outbound probes;
use `bindings probe-policy INTEGRATION_ID --delete` to remove a policy. Calls
use the candidate task's workload identity through outboundd, with normal
credential injection, route restrictions and admission budgets. Probes bypass
response caching and never include provider bodies, headers or credentials in
reports. Evidence becomes stale when credentials, probe policy, binding policy
or integration configuration change. A failed configured probe remains a
blocker even with `--allow-unsupported`.

Operators configure `outbound_probe_gateway_url = "https://outbound.example.com"`
in apid.toml and route that trusted, guest-reachable origin to outboundd. Normal
TLS verification applies. Operator-environment credentials, missing probe
policies and installations without this gateway configuration retain unsupported
coverage. A successful probe establishes only the configured endpoint. See
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
PR preview names. This target check runs before routing or waking `billing`. Preview callers
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

For an app created outside a project, use the app API instead: include
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
only for a VM whose app declares a `billing` binding. A direct request to the
service proxy with `Host: billing.internal` is checked against the same binding
inventory, so bypassing DNS cannot grant access. The existing same-account,
target allowlist, and preview checks still run before any target is woken.
Unbound `.internal` names are passed to the configured upstream DNS resolver;
Gregale does not claim the customer's entire private namespace. The existing
`*.svc.gregale` endpoint and default generated URLs remain unchanged for
rolling-upgrade compatibility; callers may opt into HTTPS-first URLs after the
listener and trust are ready. Operators may enable the private HTTPS listener
after distributing a dedicated service CA to all compute nodes.
The CA must have a critical permitted DNS name constraint for `.internal`.
When configured, guest-init builds a per-workload bundle in `/tmp`, exports
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

`gregale bindings verify <app> <service>` runs a platform-owned HTTPS canary
inside a disposable task guest attached to the caller's deployment. Use
`gregale bindings verify <app> --all` to check every declared service before
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
`gregale bindings verify <app> --postgres DATABASE_URL` with the binding's
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

`gregale bindings verify <app> --object-storage GREGALE_S3_ASSETS` checks the
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
gregale projects environments inspect shop production
gregale projects environments inspect shop production --json
gregale projects environments release-sets shop production --limit 20
```

`inspect` compares active release-set membership with each workload's ordinary
live deployment, which is what its stable environment URL currently selects.
It reports different selections and project membership gaps explicitly. A bare
`gregale projects environments inspect` uses the linked project and selected
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
gregale projects environments qualify shop staging --profile gregale-qualification.yaml
gregale projects environments preview shop --from staging --to production
gregale projects environments preflight shop --from staging --to production \
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

`gregale bindings verify` checks the private transport and current route
availability without invoking customer code. To exercise a handler on one
exact deployment, use an explicit smoke request from the caller:

```bash
gregale bindings smoke public-api billing \
  --caller-deployment v12 \
  --target-deployment TARGET_DEPLOYMENT_ID \
  --path /health \
  --expect-status 200
```

Find the target ID with `gregale traffic status billing`. `--caller-deployment`
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
workloads present at the PR head. A dependency that is absent from that
closure can still resolve to the production app, so the gateway applies the
project's production-dependency policy.

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
outright. Operators can compare isolated traffic in
`gateway_service_preview_to_preview_total` with allowed production fallbacks
in `gateway_service_preview_to_production_total`; policy rejections use
`gateway_service_call_total{outcome="preview_denied"}`.

### Verifying the caller (preview)

By default a service learns who called it from platform-set headers. Operators
who need the target to verify that claim itself can enable signed caller
assertions with `FAAS_SERVICE_CALLER_ASSERTIONS=1` on each node.

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

The signer remains opt-in and is not made a fleet-wide default by this
endpoint. Enable `FAAS_SERVICE_CALLER_ASSERTIONS=1` on every node that may
originate calls before making verified identity mandatory in a workload; while
rollout is mixed, an absent assertion is still possible and must not be treated
as verified identity.

## Internal-only ingress

Apps on every plan can be hidden from the public edge while remaining reachable
from authenticated same-account service calls. Set the visibility at create
time (`visibility: "internal"`) or update an existing app:

```bash
gregale app APP_ID --visibility internal
gregale app APP_ID --visibility public
```

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
restore in sequence unless the caller opts into wake-ahead (below).
`min_instances` remains available to trade resident RAM for first-call
latency, but it is no longer required for an internal dependency to be
reachable.

### Dependency wake-ahead

A caller that always reaches its dependencies can restore them in parallel
with its own wake instead of one hop at a time (ADR-950):

```yaml
services:
  public-api:
    build: ./public-api
    depends_on: [auth, billing]
    x-gregale-service-wake-ahead: declared
  auth:
    build: ./auth
    depends_on: [billing]
    x-gregale-service-wake-ahead: declared
  billing:
    build: ./billing
```

When `public-api` wakes for a request, Gregale starts restoring `auth` and
`billing` at the same time; `auth`'s own wake-ahead covers its edge to
`billing`. A fully cold chain then pays about one restore. Only bindings the
caller is allowed to call are restored, PR previews resolve their same-PR
siblings exactly as a real call would, and at most 8 dependencies are
restored per wake. A call that arrives during a wake-ahead joins that restore
rather than starting another.

A wake-ahead instance is an ordinary instance: it counts toward the target's
concurrency and is billed per running second until its idle timeout parks it,
even if the caller never calls it. Leave the extension off (the default) for
dependencies a request only sometimes reaches. Removing the line turns
wake-ahead off on the next deploy. These restores appear in the wake timeline
with trigger `service.wake_ahead`, and the app API returns
`service_wake_ahead`.

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
gregale bindings check public-api --deployment v12 --require-application-ack --json
gregale traffic promote --app public-api --deployment v12 --if-serving v11 --require-bindings --require-application-ack
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
gregale bindings release-policy get public-api --scope production
gregale bindings release-policy set public-api --scope production \
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
gregale bindings verify public-api --deployment v41 --all
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
gregale bindings release-policy set public-api --scope production \
  --mode off --expected-revision 1 --reason 'Restore stable deployment during incident'
```

Recover, then re-enable with the new revision. Every policy update has durable
history; disabling does not require binding/provider availability. Policy
changes and evidence are rechecked inside the traffic transaction, so a
concurrent update or expired observation cannot carry an old approval into a
traffic increase.


### Binding-checked service handoffs

A service rollout under enforcement keeps its predecessor serving while APID
checks the exact ready candidate. Missing evidence appears as bounded blockers
in `service_rollout_handoff.bindings_check`; supplying current verification lets
the worker retry without changing the policy. The routing transaction rechecks
policy, evidence and ready service capacity before publishing weights. Gateway
acknowledgement and request drain still determine handoff completion.

An exact abort uses the same pinned pair as canary recovery, including a retained
live predecessor at zero weight after service cutover. Verify that predecessor,
then request the abort and wait for the selected rollout:

```sh
gregale bindings verify public-api --deployment v41 --all
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
timeout. Add `--json` for the exact deployment response. APID restarts resume
the durable request; a missing pinned predecessor cannot be replaced silently.
See [ADR-600](adr/600-binding-checked-service-handoffs.md).

Completed deployments can be restored through an exact binding-checked rollback:

```sh
gregale rollback api --to v41 --expected-current v42 --reason "restore previous release" --wait
```

The accepted operation retains both deployments through readiness and binding
checks. The current release keeps serving while the target prepares at zero
traffic. If bindings are blocked, verify the exact target with the existing
`gregale bindings verify-all api --deployment v41` command, then inspect or resume
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
opens: same account, the caller's `depends_on` (under the `declared` policy),
the target's allowed callers, and preview scoping. A target that grants a
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
