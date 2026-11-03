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
| `faas_edgednsd_names` | `public_edge.dns.zone_id` with each of `public_edge.dns.names` |
| `faas_edgednsd_node_addresses` | each compute host's `public_ipv4`, under its registry name |
| `faas_edgednsd_dry_run` | `public_edge.dns.live`, inverted: dry run until set |

The role installs two secrets at 0400 root under `/etc/faas/secrets/edgednsd/`:

- **The zone's Cloudflare token**, Zone > DNS: Edit and nothing else, as
  `cf-token`. edgednsd reads it through `LoadCredential`. CD stages it from
  the `EDGEDNSD_CF_TOKEN` environment secret into `faas_edgednsd_cf_token`
  (`scripts/ci/edgednsd_vars.sh`). A workstation converge may set
  `faas_edgednsd_cf_token_src` to a file on the controller instead.
- **An optional env file**, `faas_edgednsd_env_src`, for example a
  `DATABASE_URL` for another login. Without it, edgednsd logs in like the
  other control-plane daemons: peer authentication as `faas` over the local
  socket. It only reads `compute_nodes`.

In dry run, edgednsd logs each change it would make and counts it in
`edge_dns_changes_total{dry_run="true"}`. Read those before going live. See
`docs/runbooks/EdgeDirectEntry.md` for the order of the whole setup.
