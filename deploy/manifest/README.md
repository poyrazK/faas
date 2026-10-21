# split-box deployment manifest (issue #911 / ADR-110)

This directory holds the Gregale split-box deployment manifest —
the source of truth for every host in a multi-box fleet. The
`gregalectl manifest validate` subcommand and the `make lint-manifest`
CI gate both consume this directory's contents.

## Files

- `examples/splitbox.example.yaml` — canonical split-box example
  (one control-plane host + one compute-only host). Load it with
  `gregalectl manifest validate --file=deploy/manifest/examples/splitbox.example.yaml`.
- `production/gcp-live.template.yaml` — deployment-specific topology template
  for the GCP fleet. Its private names and overlay network are fleet-specific;
  its provider-neutral `private_dns: managed_hosts` adapter derives private
  addresses from Ansible host facts. Release CI materializes the release tuple
  and publishes the final `production-manifest.yaml` beside the signed bundle.

## Layout

```
deploy/manifest/
├── README.md           (this file)
├── production/
│   └── gcp-live.template.yaml
└── examples/
    └── splitbox.example.yaml
```

The schema is Go-side at `pkg/manifest/`. The schema's `Validate`
function is the canonical validation path for every manifest
reader in the codebase (issue #911 explicitly requires this).
The renderer (PR-2), the release bundle installer (PR-3), and the
`gregalectl doctor` preflight (PR-4) all consume the same package.

## Quick start

```
# Validate the example manifest.
go run ./cmd/gregalectl manifest validate \
    --file=deploy/manifest/examples/splitbox.example.yaml

# Validate your own manifest.
go run ./cmd/gregalectl manifest validate --file=/path/to/splitbox.yaml

# Generate the Ansible inventory and host_vars from the same manifest.
go run ./cmd/gregalectl manifest ansible \
    --manifest-file=/path/to/splitbox.yaml \
    --output-dir=/tmp/faas-ansible
```

Use `/tmp/faas-ansible/inventory/hosts.ini` with the bootstrap playbook
or `make ANSIBLE_INVENTORY=/tmp/faas-ansible/inventory/hosts.ini
bootstrap-compute`. The generated `ansible_host` and overlay addresses
come from `fleet.hosts[].address`. The vmmd target keeps the private mTLS
identity (`vmmd.faas`) and the manifest port. With `private_dns.mode:
managed_hosts`, generated `faas_private_hosts` records are rendered into one
Ansible-managed `/etc/hosts` block on every node. The address is gathered from
the provider host fact (`ansible_default_ipv4.address` by default), or from
the provider inventory's `faas_private_address` override when the default
route is public. This is the portable source of truth; public Cloudflare DNS
is never used for private transport names. Do not copy a server IP into the
committed `deploy/ansible/host_vars` files.

Hosts with role `database` are native managed-PostgreSQL nodes (ADR-1027).
Each carries a `database` block (`admission_ceiling_mb`,
`volume_capacity_bytes`, optional `boot_slots`, `postgres_majors` and
`lifecycle`), and the fleet then needs a `managed_postgres` section:

```yaml
fleet:
  hosts:
    - name: db-1
      role: database
      address: 10.42.0.5:50051     # its vmmd; pgproxyd listens on 5432
      database:
        admission_ceiling_mb: 48000
        volume_capacity_bytes: 3000000000000
managed_postgres:
  host_suffix: db.gregale.dev    # clients connect to <database-id>.<suffix>
  shared_archive: true           # every dbbackupd shares one bucket
  app_route: true                # guests reach pgproxyd at 198.18.0.1:5432
  evacuate_draining: true        # move databases off draining nodes
  drill_every: 24h               # restore drill spacing per node (default 24h; off turns drills off)
```

The generator renders the `database_nodes` inventory group and each node's
vmmd and pgproxyd settings, including pgproxyd's cancel instance id and
peers. The control plane gets schedd's `[database_scheduler]` (written to
`/etc/faas/schedd-database.toml`) and pgproxyd's access to the verifier
table. With `app_route`, compute nodes get the route to every pgproxyd.
pgproxyd's control-plane DSN uses a password `database_node.yml` generates
once on the control plane (`/var/lib/faas/pgproxyd/postgres-password`;
`faas_pgproxyd_postgres_password` in Vault overrides it). Each node's first
PKI bundle is issued from the control-plane issuer when the node has none,
and renewed daily. Still per deployment: pgproxyd's `*.<host_suffix>`
certificate (see `deploy/ansible/database_node.yml`). Control-plane Prometheus scrapes
every database node (runbook `docs/runbooks/FaasDatabaseNodes.md`).

A node's `lifecycle` drives schedd. Set it, re-render, and converge the
control plane, which restarts schedd:
- `draining`: the node takes no new databases. With `evacuate_draining`
  (which needs `shared_archive`), schedd moves its databases off one at a
  time.
- `unavailable`: the node is lost. schedd fails its production-class
  databases over and parks the rest (`node_lost`). A point-in-time
  restore recovers those onto another node from the bucket. The
  generator also drops it from the `database_nodes` group, the scrape
  targets, the other nodes' cancel peers and the app route. Converge the
  compute and database nodes too. Its pgproxyd instance id stays reserved.

`public_edge.custom_domains` (ADR-520) turns on self-hosted certificates for
customer custom domains. It renders `faas_custom_domain_*` for the control
plane only: the `public_edge` role adds Caddy's on-demand TLS site (asking
gatewayd-public before every certificate load, issue and renewal), nftables
opens 80/443 to every source while the platform site keeps answering
Cloudflare only, and apid publishes the CNAME target and apex addresses to
customers. See `docs/ops/custom-domain-tls.md`.

The validator fails closed on every missing field, every
malformed CIDR, every non-octal mode, every non-hex digest, and
every misplaced TOML table (the load-bearing check from issue
#911).

## Schema versioning

The schema is SemVer (`schema_version: 1.0.0` in the manifest
root). The validator refuses any manifest whose `schema_version`
is not in `pkg/manifest.SupportedSchemaVersions`. Bumping the
major version is a breaking change (mandatory field, renamed key,
tightened enum); minor + patch are backward-compatible.

## Required fields

See `pkg/manifest/manifest.go` for the canonical schema. The
validators are exhaustive — every required field generates a
`path: is required` error if missing.

## TOML table-placement

The renderer (PR-2) writes one TOML per daemon on each host. The
load-bearing invariant from issue #911 is that keys belong to the
right table: vmmd's `[compute_node]` block must NOT re-declare
the top-level cluster (the bug at
`deploy/ansible/roles/vmmd_service/files/vmmd.toml.example`
lines 33-40 — the canonical top-level `tls_cert_path` /
`tls_key_path` / `tls_ca_path` cluster),
and `[compute_node]` must NOT carry remote-daemon keys (e.g.
`schedd_client_*`). The
catalog at `pkg/manifest/toml_check.go` is the source of truth
for which key belongs to which table; the validator and the
renderer both consume it.
