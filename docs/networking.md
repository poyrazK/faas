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

The name is the app slug. Declare the edges with `depends_on` and Gregale
injects the URLs for you, so nothing hard-codes a hostname:

```yaml
services:
  public-api:
    depends_on: [auth, billing, recommendation]
```

`public-api` then starts with `GREGALE_SERVICE_AUTH_URL`,
`GREGALE_SERVICE_BILLING_URL`, and `GREGALE_SERVICE_RECOMMENDATION_URL` in its
environment. The dependency graph is validated before anything deploys —
unknown names, self-edges, and ambiguous names are rejected.

The same declared edges are exposed as service bindings by the app API and by
`gregale bindings public-api`, alongside database, object-storage, and queue
bindings. A service binding is currently a discovery and deploy-order
declaration, not a network allowlist: omitting an edge does not deny traffic.

Calls are authorized by the platform, not by your code. The caller is
identified from the network identity of the calling VM, so a guest cannot
claim to be another app, and the proxy only permits calls between apps in the
same account. Cross-account calls are refused.

### Preview-to-production service policy

A pull-request preview is provisioned as **one app**, derived from the app the
PR touches. It does not get its own copy of that app's dependencies, and
service names resolve without an environment scope. When permitted, a
preview's internal calls therefore reach your **production** services.

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

In `allow_marked` mode, Gregale marks these calls so a service can react rather
than be surprised. Every request from a preview app carries:

```text
X-Faas-Caller-Env: preview
X-Faas-Caller-Preview-Of: public-api
```

Both headers are platform-owned: anything a workload sends under those names is
stripped before the hop, so the marker cannot be forged. Production callers
carry neither header, so a service that ignores them is unaffected.

Use them to skip irreversible work, tag writes as test data, or refuse the call
outright. Operators can watch allowed traffic with
`gateway_service_preview_to_production_total` and policy rejections with
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

Pro and Scale apps can be hidden from the public edge while remaining reachable
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
