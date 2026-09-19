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
fail-closed, so they never imply reachability; a later fabric reconciler is the
only component allowed to mark one `ready`. A network referenced by a peering
cannot be deleted until that peering is removed.

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
`http://APP_ID.svc.gregale:10080`. For external VPC resources, Pro and Scale
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
replays the effective policy to every live node, and nftables keeps traffic
blocked until each update succeeds. Empty rule lists preserve the legacy
CIDR-only behavior; the PUT body replaces both lists, so include a list when
you intend to retain an existing restriction.

Schedd applies a ready attachment once per live compute node and records a
per-node convergence observation in its reconciliation logs. A partial node
failure keeps the attachment in `error` and is retried by the next sweep;
successful nodes are still reported so operators can identify the unhealthy
box without guessing from aggregate status.

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
`APP_ID.svc.gregale:10080`, where the service proxy enforces caller identity
and same-account authorization. Visibility changes are audited and invalidate
the gateway route cache.
