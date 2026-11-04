# edgednsd_service

Runs `edgednsd` on the control plane (ADR-20261102081618 §3). It keeps each
managed app host name's A records pointed at the compute nodes whose edges
are ready. A name's records are proxied by Cloudflare, or DNS-only so clients
connect to the nodes themselves (ADR-20261103163954 §1); a name's entry in
`faas_edgednsd_names` ending in `:dns-only` selects that, and edgednsd
switches the existing records of a name whose mode changed.

The manifest's `public_edge.dns` turns it on and renders its settings:

| Variable | From |
|---|---|
| `faas_edgednsd_names` | `public_edge.dns.zone_id` with each of `public_edge.dns.names`, `:dns-only` under `public_edge.front: none`, and each of `public_edge.dns.proxied`, proxied |
| `faas_edgednsd_node_addresses` | each compute host's `public_ipv4`, or its `entry_ipv4` under `public_edge.front: none`, under its registry name |
| `faas_edgednsd_dry_run` | `public_edge.dns.live`, inverted: dry run until set |
| `faas_edgednsd_certificate_names`, `faas_edgednsd_certificate_zone_id`, `faas_edgednsd_acme_ca` | `public_edge.dns.certificate.names`, `public_edge.dns.zone_id` and `public_edge.dns.certificate.ca` (`letsencrypt-staging` by default) |

The role installs two secrets at 0400 root under `/etc/faas/secrets/edgednsd/`:

- **The zone's Cloudflare token**, Zone > DNS: Edit and nothing else, as
  `cf-token`. edgednsd reads it through `LoadCredential`. CD stages it from
  the `EDGEDNSD_CF_TOKEN` environment secret into `faas_edgednsd_cf_token`
  (`scripts/ci/edgednsd_vars.sh`). A workstation converge may set
  `faas_edgednsd_cf_token_src` to a file on the controller instead.
- **An optional env file**, `faas_edgednsd_env_src`, for example a
  `DATABASE_URL` for another login. Without it, edgednsd logs in like the
  other control-plane daemons: peer authentication as `faas` over the local
  socket. It reads `compute_nodes` and, with a certificate configured,
  writes `edge_tls_certificates`.

## The apps domains' certificate

With `faas_edgednsd_certificate_names` set (for example `["*.gregale.dev"]`),
edgednsd also keeps the apps domains' publicly trusted certificate
(ADR-20261103163954 §3). It orders the certificate from Let's Encrypt with
the DNS-01 challenge in the zone `faas_edgednsd_certificate_zone_id`, with the
same token, and renews it 30 days before it expires. Each certificate gets a
new ECDSA P-256 key, sealed to the `edge-tls` seal domain before it is
stored; only the compute edges hold that identity, so edgednsd cannot read
the key back.

- `faas_edgednsd_acme_ca` is `letsencrypt-staging` until the path is proven,
  then `letsencrypt`.
- The role creates the ACME account key once, at
  `/etc/faas/secrets/edgednsd/acme-account.key` (0400 root), and never
  replaces or removes it: the zone's CAA records name that account. Back it
  up with the other control-plane secrets.
- The `edge-tls` recipient must be staged first
  (`gregalectl seal-domain init --domain edge-tls`, then the seal_domains
  role); the role refuses to configure a certificate without it.
- `edge_certificate_acme_account_info{account_uri}` reports the account URI
  for the CAA records, and `edge_certificate_orders_total{result}` and
  `edge_certificate_stored_expiry_timestamp_seconds` how ordering goes.

In dry run, edgednsd logs each change it would make and counts it in
`edge_dns_changes_total{dry_run="true"}`. Read those before going live. See
`docs/runbooks/EdgeDirectEntry.md` for the order of the whole setup.
