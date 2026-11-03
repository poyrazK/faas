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

## 3. Turn on zone-level Authenticated Origin Pulls

```bash
CF_ZONE_ID=<zone id> deploy/scripts/cloudflare-origin-pull.sh plan
CF_ZONE_ID=<zone id> APPLY=1 deploy/scripts/cloudflare-origin-pull.sh upload ~/gregale-origin-pull
```

The token needs Zone > SSL and Certificates: Edit, and Zone > Config Rules:
Edit. Cloudflare then presents the client certificate to every origin in the
zone. Origins that do not ask for one, such as the control plane's Caddy and
Vercel, ignore it.

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

## Undo

- **Pilot:** remove `public_edge.dns` and release, then delete the pilot host's
  A records. Their comment reads `edgednsd (ADR-20261102081618)`.
- **Listeners:** set `public_edge.entry: tunnel` and release.
- **GCP addresses:** `APPLY=1 gcp-compute-edge-address.sh delete NODE...`.
- **Configuration rule:** delete it in the dashboard (Rules > Configuration Rules).
- **Zone-level Authenticated Origin Pulls:** turn it off in the dashboard (SSL/TLS > Origin Server).
