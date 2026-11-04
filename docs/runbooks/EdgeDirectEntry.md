# Edge direct entry: Cloudflare connects to every compute node (ADR-20261102081618)

With the tunnel, Cloudflare sends almost every app request to one node's
connector. Direct entry gives each compute edge a TLS listener on port 443
and points proxied address records at every node, so the entry work spreads.
This runbook prepares the nodes and the zone. It changes no app traffic: until
DNS points an app host at the nodes, every request still enters through the
tunnel.

Run the steps in order. Each script prints what it would change and writes
only with `APPLY=1`.

## 1. Make the origin-pull CA and the zone's client certificate

On an operator workstation:

```bash
deploy/scripts/cloudflare-origin-pull.sh ca ~/gregale-origin-pull
```

This writes four files:
- `origin-pull-ca.pem`: the CA the edges trust. It goes to the compute nodes in step 2.
- `origin-pull-client.{pem,key}`: the client certificate Cloudflare will present. It is uploaded in step 3.
- `origin-pull-ca.key`: keep it offline, or delete it. A new pair needs a new CA anyway.

## 2. Stage the TLS material on every compute node

Each node needs three files, root-owned and mode 0600, which the edge reads
through systemd `LoadCredential`:

| File | Source |
|---|---|
| `/etc/faas/secrets/edge-tls/origin.pem` | the control plane's `/etc/caddy/tls/cloudflare-origin.pem` (the zone's Origin CA certificate; it covers `*.gregale.dev` until 2041) |
| `/etc/faas/secrets/edge-tls/origin.key` | the control plane's `/etc/caddy/tls/cloudflare-origin.key` |
| `/etc/faas/secrets/edge-tls/origin-pull-ca.pem` | `origin-pull-ca.pem` from step 1 |

Never put them in the repository or in inventory.

**Or the certificate edgednsd keeps** (ADR-20261103163954 §3). With nothing
in front, the edge needs a publicly trusted certificate for the apps domain.
edgednsd orders it from Let's Encrypt and stores its key sealed to the
`edge-tls` seal domain; each edge loads it with
`faas_edge_public_tls_cert_source: database` and opens the key in memory
with the domain's identity, so no certificate or key file sits on the node.
Order first, then switch the edges:

1. `gregalectl seal-domain init --domain edge-tls`, then the seal_domains
   role: the recipient on the control plane, the identity on compute nodes.
   CD stages them from the `SEAL_DOMAIN_EDGE_TLS_AGE_KEY` and
   `SEAL_DOMAIN_EDGE_TLS_AGE_RECIPIENT` environment secrets.
2. In the manifest, `public_edge.dns.certificate.names` (for example
   `["*.gregale.dev"]`); `ca` stays `letsencrypt-staging` until the path is
   proven. Converge the control plane and wait for
   `edge_certificate_orders_total{result="issued"}`.
3. `public_edge.dns.certificate.serve` set to that CA, then converge the
   compute nodes: each edge serves the certificate stored from it. An edge
   is not ready until it has loaded one, so edgednsd keeps it out of DNS
   until then.
4. To move to production, set `ca: letsencrypt` and converge the control
   plane; only once `edge_certificate_orders_total{ca="letsencrypt",
   result="issued"}` counts it, set `serve: letsencrypt` and converge the
   compute nodes. Changing both at once would restart every edge on a CA it
   has no certificate from yet.
5. `public_edge.dns.certificate.transparency` turns on the Certificate
   Transparency watch: `domain` (the apps domain) and `allow_names`, the
   names other parties' certificates may carry without a page. For
   gregale.dev today: `gregale.dev` and `*.gregale.dev` (Cloudflare's own
   edge certificate, which platform hosts still use) and
   `operations.gregale.dev` (hosted elsewhere). Turn it on with step 2, before
   the edges serve the certificate.

Only `origin-pull-ca.pem` is still staged by hand. Alerts and failures:
[FaasEdgeCertificate](FaasEdgeCertificate.md).

## 3. Turn on zone-level Authenticated Origin Pulls

```bash
CF_ZONE_ID=<zone id> deploy/scripts/cloudflare-origin-pull.sh plan
CF_ZONE_ID=<zone id> APPLY=1 deploy/scripts/cloudflare-origin-pull.sh upload ~/gregale-origin-pull
```

The token needs Zone > SSL and Certificates: Edit, and Zone > Config Rules:
Edit. Cloudflare then presents the client certificate to every origin in the
zone. Origins that do not ask for one, such as the control plane's Caddy and
Vercel, ignore it.

### The control plane's Caddy

The control plane's platform site needs the same certificate check
(ADR-20261103172551): it admits Cloudflare's address ranges, which every
Cloudflare customer's zone also uses. The public_edge role refuses to deploy
without it.

1. Upload the client certificate to every zone the site serves (the platform,
   apps, deployment-preview and alias domains), all signed by the same CA.
2. Install `origin-pull-ca.pem` on the control plane at
   `/etc/caddy/tls/cloudflare-origin-pull-ca.pem` (root-owned, 0644).
3. Set `dns.origin_pull: required` in the manifest and deploy (it renders
   `faas_public_edge_authenticated_origin_pulls: true`).

Never install Cloudflare's shared origin-pull CA there: Cloudflare presents
that certificate for every zone, and the role refuses it. To deploy before
the certificate is in place, set `dns.origin_pull: any_zone` (it renders
`faas_public_edge_allow_any_cloudflare_zone: true`), and change it to
`required` once the check is on.

## 4. Check the edges' certificate for app hosts

```bash
CF_ZONE_ID=<zone id> APPLY=1 deploy/scripts/cloudflare-origin-pull.sh strict
```

This adds a configuration rule that sets SSL to "Full (strict)" for
`*.<apps domain>`, except the platform hosts. Hosts on the tunnel are not
affected, because the tunnel ignores the SSL mode. The rest of the zone keeps
Cloudflare's automatic mode.

## 5. GCP only: an inbound-only address per node

```bash
GCP_PROJECT=gregale-prod deploy/scripts/gcp-compute-edge-address.sh plan gregale-compute-1 gregale-compute-2
GCP_PROJECT=gregale-prod APPLY=1 deploy/scripts/gcp-compute-edge-address.sh apply gregale-compute-1 gregale-compute-2
```

For each node, the script:
- tags it `gregale-origin`, the VPC's existing Cloudflare-only 80/443 rules;
- reserves a static address;
- forwards that address's TCP 443 to the node through a target instance.

The node keeps Cloud NAT for everything it sends. Record the printed
addresses: DNS points at them later.

On each node, check that the guest agent added the address to the local routes:

```bash
ip route show table local | grep <address>
```

## 6. Open the listeners

Set `public_edge.entry: both` in the cell's manifest and release. Each compute
edge then listens on port 443 beside the tunnel listener. Its firewall admits
80/443 from Cloudflare's ranges only, both at boot and after vmmd's rebuilds.
If the step 2 files are missing, the role fails before it changes the edge.

Check each node:

```bash
ss -ltn 'sport = :443'
```

Also watch `gatewayd_public_edge_tls_handshake_failures_total`. A steady rise
means Cloudflare is not presenting the zone's client certificate.

## 7. edgednsd, dry run first

`edgednsd` runs on the control plane. It keeps each managed name's proxied A
records pointed at the compute nodes whose edges are ready. It logs in to
Postgres like the other control-plane daemons, and needs one secret: a
Cloudflare token with Zone > DNS: Edit on the apps zone only. Store it as the
deploy environment's `EDGEDNSD_CF_TOKEN` secret; CD hands it to the role:

```bash
gh secret set EDGEDNSD_CF_TOKEN --env production-us --repo poyrazK/faas
```

In the manifest, add each compute host's address and the pilot name:

```yaml
fleet:
  hosts:
    - name: fsn-2
      role: compute-only
      public_ipv4: <address from step 5>
public_edge:
  entry: both
  dns:
    zone_id: <zone id>
    names: [<pilot app host>]
```

Release, then watch `edge_dns_changes_total{dry_run="true"}` and edgednsd's
log. Each "would create" line should name one ready node's address for the
pilot host.

## 8. Pilot

Set `public_edge.dns.live: true` and release. edgednsd creates the pilot
host's records, which take precedence over the wildcard, so only that host
leaves the tunnel. Run the ADR's step 0 questions against it. Remove each
injected failure as soon as it is measured.

## Client limits (ADR-20261103163954 §4)

`faas_edge_client_limits: true` on the edge role sets
`FAAS_EDGE_CLIENT_LIMITS=1`. The values are in `pkg/api/limits.go`
(`EdgeClient*`, `EdgeTLSHandshake*`); each is per node and per client address
(an IPv4 address or an IPv6 /64).

- **Requests a second**, on any front. Behind Cloudflare the client is
  `CF-Connecting-IP`; with no front it is the connection's peer, whatever the
  request claims. A refusal is `429 rate_limited` with `Retry-After: 1`,
  `x-faas-rate-limit-scope: client-address`, the limit and the observed value.
- **Requests a second per hostname**, on any front, per node, before the
  request is forwarded or wakes anything (`EdgeHostRequests*`): twice the
  highest plan's per-app limit, so the gateway's plan limit answers an app
  first. A refusal is `429 rate_limited` with
  `x-faas-rate-limit-scope: host`.
- **Open connections, new connections a second and TLS handshakes in
  progress**, only with `faas_edge_public_front: none`. A refused connection
  is reset before TLS; a handshake slot is held at most
  `EdgeTLSHandshakeTimeout`.

- **A minimum request-body rate**, on any front: a body must bring
  `EdgeClientBodyMinBytesPerWindow` bytes every `EdgeClientBodyRateWindow`,
  or its read fails. gRPC client streams are exempt.
- **A cap on requests forwarded to the control plane** (uploads, public
  reads, OTLP), `EdgeControlPlaneForwardsInFlightMax` at once per edge; past
  it the edge answers `503 control_plane_busy` with `Retry-After: 1`.

`gatewayd_public_edge_refused_total{reason}` counts refusals by
`open_connections`, `connection_rate`, `handshakes`, `request_rate`,
`host_request_rate`, `body_rate` and `forward_in_flight`. A
burst on one reason from many addresses is a flood; a steady stream from one
address may be carrier-grade NAT, where the starting values need raising.
An edge with no front and the limits off logs a warning at start.

### The kernel backstop

On a node with entry addresses (`faas_public_entry_addrs`, vmmd's
`FAAS_PUBLIC_ENTRY_ADDRS`) the host firewall also drops a source's new
connections to an entry address's 443 before the edge sees them, at twice
the edge's own limits (`PublicEntry*` in `pkg/api/limits.go`): open
connections per source, and new connections a second per source.
Cloudflare's ranges are exempt when the node keeps them
(`faas_cloudflare_origin_only`, which `front: none` keeps on): a host moved
back behind Cloudflare arrives from a few of its addresses, each carrying many
clients. The drops are counted on their rules:

```bash
sudo nft list chain inet faas input | grep 'per source'
```

The nftables role also raises the conntrack table to
`faas_public_entry_conntrack_max`, shortens its `SYN_RECV` timeout to
`faas_public_entry_conntrack_syn_recv_timeout` seconds and deepens the SYN
queue, so a spoofed SYN flood, which per-source limits cannot stop, does not
fill the conntrack table that tenant NAT shares. Watch
`node_nf_conntrack_entries` against `node_nf_conntrack_entries_limit`.

### Block a source on the entry addresses (ADR-20261103163954 §2)

On a node with entry addresses, vmmd keeps `public_block_v4/v6`, sets the
firewall checks before anything else on those addresses, so a blocked
source's open connections stop too. List each range with an expiry in
`/etc/faas/public-block-list` on every compute node, one per line:

```text
# <prefix in canonical form> <expiry, RFC 3339> [reason]
192.0.2.0/24 2026-11-04T18:00:00Z SYN flood, ticket 123
2001:db8:1234::/48 2026-11-05T00:00:00Z
```

vmmd re-reads the file every 30 s (`PublicBlockListRefreshInterval`) and
after every ruleset reload, which would otherwise flush the sets; a
malformed line is skipped and logged (`journalctl -u faas-vmmd | grep 'public
block list'`). An entry lasts at most 90 days. Check:

```bash
sudo nft list set inet faas public_block_v4
sudo nft list chain inet faas input | grep 'block list'
```

To unblock before the expiry, delete the line and the element:
`sudo nft delete element inet faas public_block_v4 { 192.0.2.0/24 }`.
Lengthening an entry's expiry takes effect when the old one lapses (at most
30 s later). A hand-made `nft` rule does not survive vmmd's next reload; use
the file.

## Nothing in front (ADR-20261103163954), pilot

Clients reach the pilot host straight at the compute nodes, with DNS-only
records. The owner decides each step marked **owner**.

1. **owner:** approve `access.direct_entry` in
   `deploy/gcp/public-beta-policy.json`: one VPC rule admits TCP 443, and
   only 443, from every source to instances tagged `gregale-entry`, compute
   nodes only. `scripts/ops/gcp_public_beta_audit.py` fails on anything
   wider, and on any other rule that opens 80 or 443 to everyone.
2. **owner:** run `deploy/scripts/gcp-compute-edge-address.sh entry NODE...`,
   read what it would do, then rerun with `APPLY=1`. It creates
   `gregale-direct-entry-v4` once and tags each node. The forwarding rule from
   step 5 must already exist: it carries 443 alone, so the node's own
   addresses stay unreachable.
3. **owner:** put a publicly trusted certificate for the apps domain (a
   wildcard, from a DNS-01 challenge) at the edge role's
   `faas_edge_public_tls_cert` and key paths on every compute node. Cloudflare
   still accepts it for the proxied names.
4. In the manifest, mark each compute host's forwarded address and turn the
   front off:

   ```yaml
   fleet:
     hosts:
       - name: fsn-2
         role: compute-only
         public_ipv4: <address from step 5>
         public_ipv4_forwarded: true
   public_edge:
     entry: both
     front: none
     dns:
       zone_id: <zone id>
       names: [<new pilot app host>]
       live: true
   ```

   Release only once every compute node runs the release that has the entry
   addresses (#6501). The vmmd drop-in also sends Cloudflare's ranges, so an
   older vmmd stays Cloudflare-only rather than open.
5. Check: `nft list chain inet faas input` on a node shows the
   `public entry` accept and drop ahead of sshd; `curl -v
   https://<pilot host>` from outside reaches the node without Cloudflare
   (`server` and the certificate issuer show it); `dig` returns the nodes'
   addresses with TTL 60.

To put one host back behind Cloudflare, add it to `public_edge.dns.proxied`
and release: edgednsd switches its records to proxied in place.

## Under attack: back behind Cloudflare (ADR-20261103163954)

With nothing in front, the node's provider absorbs network floods and the
edge's per-address limits shed what one address sends. A flood that fills the
entry addresses, or an application flood from many addresses, needs Cloudflare
back in front. The fallback works because:

- the edge still verifies the zone's client certificate when given, and then
  takes the client from `CF-Connecting-IP`, so request limits stay per client
  (keep zone-level Authenticated Origin Pulls on, step 3, so Cloudflare
  presents it);
- Cloudflare's ranges skip the per-address connection limits, in the edge
  (`FAAS_EDGE_PROXY_CIDRS`) and in the host firewall's backstop
  (`public_entry_proxy_v4/v6`), as long as the node keeps
  `faas_cloudflare_origin_only`, which `front: none` keeps on;
- the edge's certificate is publicly trusted, which Cloudflare's Full (strict)
  accepts;
- on GCP, `gregale-cloudflare-origin-v4/v6` still admit Cloudflare on 443 to
  the `gregale-origin` tag, with or without `gregale-direct-entry-v4`.

Fastest first. Each step is the operator's (**owner**); it needs the zone
token on the control plane, which only edgednsd reads.

1. **One host or every app host, minutes:** on the control plane, set the
   affected entries of `FAAS_EDGEDNS_NAMES` in
   `/etc/systemd/system/faas-edgednsd.service.d/50-faas-edgednsd.conf` from
   `:dns-only` to `:proxied` (the wildcard entry moves every app host), then
   `systemctl daemon-reload && systemctl restart faas-edgednsd`. edgednsd
   switches the records in place on its next pass. DNS-only records carry a
   60 s TTL, so resolvers follow within about a minute; clients holding
   connections to the entry addresses keep them until they reconnect. Record
   the same names under `public_edge.dns.proxied` in the manifest before the
   next release, which rewrites that file. Do not flip records in the
   dashboard while edgednsd runs: it switches them back.
2. **The entry addresses themselves are the target** (attackers who learned
   them keep sending after the records are proxied):
   `APPLY=1 deploy/scripts/gcp-compute-edge-address.sh unentry NODE...`
   untags the nodes, and Google's firewall drops everything but Cloudflare on
   443 before it reaches them. Proxied hosts keep working; DNS-only ones stop,
   so do step 1 for every app host first.
3. **Stay behind Cloudflare:** set `public_edge.front: cloudflare` and
   release. The edge then requires the client certificate again.

Check after each step: `dig +short <host>` returns Cloudflare's addresses
(step 1); `gatewayd_public_edge_refused_total` and the firewall's `per source`
counters fall; `curl -sI https://<host>` shows `server: cloudflare` and
`cf-ray`. Undo in reverse: `entry` again, then the names back to `:dns-only`.

Rehearse steps 1 and 2 on the pilot host before the wildcard leaves
Cloudflare, and time them (ADR-20261103163954).

## Undo

- **Nothing in front:** set `public_edge.front: cloudflare` (or remove it)
  and release; edgednsd switches the names' records back to proxied. Then
  `APPLY=1 gcp-compute-edge-address.sh unentry NODE...`, and delete
  `gregale-direct-entry-v4` once no node carries the tag.

- **Pilot:** remove `public_edge.dns` and release, then delete the pilot host's
  A records. Their comment reads `edgednsd (ADR-20261102081618)`.
- **Listeners:** set `public_edge.entry: tunnel` and release.
- **GCP addresses:** `APPLY=1 gcp-compute-edge-address.sh delete NODE...`.
- **Configuration rule:** delete it in the dashboard (Rules > Configuration Rules).
- **Zone-level Authenticated Origin Pulls:** turn it off in the dashboard (SSL/TLS > Origin Server).
