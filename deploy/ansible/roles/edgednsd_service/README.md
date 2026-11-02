# edgednsd_service

Runs `edgednsd` on the control plane (ADR-20261102081618 §3). It keeps each
managed app host name's proxied A records pointed at the compute nodes whose
edges are ready, so Cloudflare spreads app traffic over every node.

The manifest's `public_edge.dns` turns it on and renders its settings:

| Variable | From |
|---|---|
| `faas_edgednsd_names` | `public_edge.dns.zone_id` with each of `public_edge.dns.names` |
| `faas_edgednsd_node_addresses` | each compute host's `public_ipv4`, under its registry name |
| `faas_edgednsd_dry_run` | `public_edge.dns.live`, inverted: dry run until set |

The operator provides two files on the controller, which the role installs at
0400 root under `/etc/faas/secrets/edgednsd/`:

- `faas_edgednsd_env_src`: `DATABASE_URL` for a `faas_runtime` member that may
  read `compute_nodes`;
- `faas_edgednsd_cf_token_src`: the zone's Cloudflare token, Zone > DNS: Edit
  and nothing else. edgednsd reads it through `LoadCredential`.

In dry run, edgednsd logs each change it would make and counts it in
`edge_dns_changes_total{dry_run="true"}`. Read those before going live. See
`docs/runbooks/EdgeDirectEntry.md` for the order of the whole setup.
