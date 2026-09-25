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
`http://APP_SLUG.svc.gregale:10080`. For external VPC resources, Pro and Scale
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

## Internal services

Apps in the same account reach one another by name, on every plan. There is no
VPC, subnet, security group, internal load balancer, service registry, or DNS
record to configure:

```text
public-api  ──►  auth
            ├─►  billing
            └─►  recommendation
```

Each dependency is an ordinary app. From `public-api`, call them as:

```text
http://auth.svc.gregale:10080
http://billing.svc.gregale:10080
http://recommendation.svc.gregale:10080
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
an explicit HTTPS alias. A caller can opt into HTTPS-first transport for all
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
`gregale bindings public-api`, alongside database, object-storage, and queue
bindings. New project workloads use the `declared` caller policy: the gateway
returns 403 for calls to services not listed in `depends_on`. Existing apps
retain their persisted policy on reapply. For an intentional same-account
escape hatch, set `x-gregale-service-policy: account` on the caller. The CLI
reports declared service bindings as `enforced`.

The target can independently restrict who calls it with
`x-gregale-allow-callers`. In the example, `billing` admits `public-api` but
not other same-account apps, even if they declare a dependency on `billing`.
An omitted list preserves legacy same-account reachability; `[]` denies every
internal caller. The list accepts at most 100 logical app slugs, not generated
PR preview names. This target check runs before routing or waking `billing`. Preview callers
must also pass the existing project and target preview policies.

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
  "service_binding_policy": "declared"
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
Project-managed and preview apps reject changes to these fields on PATCH; edit the
project source instead. Binding declarations do not expose services publicly.

Bound services can also be called through the short private alias
`http://billing.internal:10080`. Gregale's node-local DNS answers that name
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
switching the caller to HTTPS-first transport. The all-bindings form reports
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
to the API guest. Forward it on outbound managed service calls. The service
proxy verifies the calling VM's deployment belongs to that release and picks
the matching target deployment; it cannot be spoofed into selecting a graph
from a caller header alone. When the same API deployment belongs to several
unexpired sets, an internal call without the release header returns 409 rather
than guessing. The active set does not expire; its TTL starts when a new set
replaces it. Publish a new complete set whenever project membership changes.

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

### Smoke-test a downstream deployment

To test a live deployment of a bound service before giving it traffic, send
its deployment ID on that managed service request:

```bash
curl -H 'Gregale-Target-Deployment: DEPLOYMENT_ID' \
  http://billing.svc.gregale:10080/health
```

Find the ID with `gregale traffic status billing`. This works for a live
deployment at 0% traffic: the service proxy wakes that exact deployment if
needed. The override wins over `Gregale-Version-Key` for this one service hop,
but the version key remains available to the target app. The override header
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

Preview provisioning currently creates **one app**, derived from the app the
PR touches, rather than cloning the whole project. A same-PR dependency may
therefore be absent. In that case the gateway considers the production app
and applies the project's production-dependency policy.

New projects default to `preview_service_policy: deny`. A denied call returns
`403 application/problem+json` with code
`preview_production_dependency_denied` before the proxy discovers or wakes the
target. Projects that existed when this policy shipped were migration-backed
to `allow_marked`, preserving their live behaviour. Opt an existing project
into isolation with:

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

Guest-reachable key publication and a runtime verification helper are not
shipped yet, so this is currently useful for operators wiring their own
verification. Leave the flag off otherwise.

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
`APP_SLUG.svc.gregale:10080`, where the service proxy enforces caller identity
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
ordinary gRPC client against `http://APP_SLUG.svc.gregale:10080`. Response
trailers — including `grpc-status` — are preserved across the hop.
`Connection: Upgrade` requests (WebSocket and friends) take the verbatim-bytes
bridge and are neither buffered nor retried; they require the target app to
have WebSockets enabled and return `501` otherwise. Non-HTTP raw TCP between
services is not part of the discovery contract: address those listeners
through named ports instead.
