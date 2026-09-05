# `deploy/ansible/` — host bootstrap

`bootstrap.yml` is the only production host bootstrap playbook. It has
separate control-plane and compute-only plays and refuses a single-box role.

The observability overlay is `site.yml`. It expects a dedicated
`[observability]` inventory group for Loki, then installs Promtail on the
control-plane and compute groups. This separation keeps a FaaS host loss from
also deleting its incident logs:

```
make ANSIBLE_INVENTORY=/path/to/inventory.ini bootstrap-observability
```

Set `promtail_loki_url` and the Loki/Promtail mTLS file paths in provider-owned
inventory variables. For backend meta-monitoring, also set
`prom_loki_metrics_target`, `prom_loki_metrics_server_name`, and the three
`prom_loki_metrics_*_file` paths on the control plane. The Prometheus role then
scrapes Loki's `/metrics` endpoint over mTLS and enables the Loki backend
alerts.

To enable Grafana log queries, set `gv_loki_url`, `gv_loki_server_name`, and
`gv_loki_tenant_id`. Provision the provider-owned
`/etc/grafana/secrets/loki.env` as mode `0400` or `0440` before the Grafana
role runs. It must provide the PEM values as `FAAS_LOKI_TLS_CA_CERT`,
`FAAS_LOKI_TLS_CLIENT_CERT`, and `FAAS_LOKI_TLS_CLIENT_KEY`; the role passes
these to Grafana through its systemd environment without reading or storing
the certificate contents. Secret contents are never stored in this
repository.

## What it does

In order (each role is independent and verifies its own preconditions):

| Role | Spec § | What it touches | Idempotent because |
|---|---|---|---|
| `compute_admission` | §4.4 / §11 | compute env, `/dev/kvm`, release manifest | fail-closed assertions before provisioning |
| `cgroups_v2` | §11 | asserts kernel cmdline | verify-only |
| `grub` | §11 | `/etc/default/grub`, sysctl | `creates:` sentinel, regex match |
| `storage` | §8 | provider-neutral `/srv/fc` filesystem and directory contract | reuses valid mounts; initializes only eligible blank devices |
| `lvm` | §8 | verify lv-system / lv-fc when the reference layout is selected | verify-only |
| `xfs` | §8 | dedicated fast-root mount, XFS features, `/srv/fc/jail` tmpfs | explicit device contract + `/etc/fstab` |
| `firecracker` | §4.4 | `/usr/local/bin/{firecracker,jailer}`, `/srv/fc/base/vmlinux-6.1` | content checksums + config-aware rebuild |
| `systemd_slices` | §13 | three `.slice` unit drops | `creates:` on each |
| `nftables` | §7 | `/etc/nftables.conf` | managed-marker backup + `nft -c` syntax check |
| `postgres` | §1 (cp slice), §4 | distro PostgreSQL major, `faas` user | apt idempotent, `creates:` on home |
| `host_hardening` | §11, ADR-143 | sshd drop-in, fail2ban, unattended security upgrades, auditd rules, kernel sysctls | templates + validated `sshd -t`; lockout guard before disabling password auth |
| `geoip` | ADR-091 D21, ADR-143 | `/var/lib/faas/geoip/dbip-country-lite.mmdb` (compute) | pinned monthly release + two SHA-256s |
| `fleet_verify` | ADR-143 | verify-only: enabled → active → probe → `gregalectl doctor` | read-only |

## Run it

```
sudo apt update && sudo apt install -y ansible git
git clone <repo> faas && cd faas
make manifest-ansible MANIFEST=/path/to/splitbox.yaml
make ANSIBLE_INVENTORY=deploy/ansible/.generated/inventory/hosts.ini bootstrap-control-plane
make ANSIBLE_INVENTORY=deploy/ansible/.generated/inventory/hosts.ini bootstrap-compute
```

Every bootstrap target first runs `ansible-preflight` against the complete
inventory. This is intentional: a role-limited run still renders the same
private endpoint map on every host, so a missing peer address must stop the
deployment before any host is changed. Run it explicitly when validating a
new provider or inventory:

```
make ANSIBLE_INVENTORY=deploy/ansible/.generated/inventory/hosts.ini ansible-preflight
make ANSIBLE_INVENTORY=deploy/ansible/.generated/inventory/hosts.ini ansible-syntax-check
```

After the operator has run `gregalectl secrets init` and enabled the units,
prove the fleet converged (every required daemon enabled, active and
answering its readiness probe, then `gregalectl doctor`):

```
make ANSIBLE_INVENTORY=deploy/ansible/.generated/inventory/hosts.ini verify-fleet
```

Both bootstrap plays end with the same `fleet_verify` role in lenient mode,
so a re-run after a config change fails loudly when a daemon did not come
back. Every unit, drop-in and config task notifies a `try-restart` handler
(ADR-143): active daemons pick the change up, disabled ones are left alone.

## Configuration contract (ADR-143)

- Systemd units are generated from `pkg/daemonunitspec` into every role's
  `files/` tree; never hand-edit a `faas-*.service` — run `make generate`
  and commit. `make generate-check` gates it.
- Every `FAAS_*` a daemon reads is declared in
  `pkg/daemonunitspec/envcontract.go` with who delivers it; the rendered
  table is [`docs/ops/env-contract.md`](../../docs/ops/env-contract.md) and
  `make env-contract-check` (part of `make test`) fails on an undeclared,
  undelivered or dead variable.
- `vars/daemons.yml` is the daemon topology shared by `role_convergence`
  and `fleet_verify`; a Go test pins it to the registry.
- `make ansible-lint` runs ansible-lint at the `production` profile with
  the config in `.ansible-lint`; `make ansible-scale-check` executes
  `scale_check.yml` (not just `--syntax-check`) against the rendered
  example manifest. Both run in CI.

Before connecting to any provider, run the connection-free scale gate:

```
make manifest-scale-check
```

It renders and validates 1, 10, 100, and 1,000 compute-node topologies and
runs `scale_check.yml` in Ansible check-mode against the largest generated
inventory. The manifest contract currently supports up to 1,000
`compute-only` hosts. For fleets larger than 32 hosts, the shared private
resolver map is emitted once in `inventory/group_vars/all.yml`; host-specific
transport and routing values remain in `host_vars`.

SSH host-key checking is enabled by default. For a direct `join-node` run,
provide `--ssh-host-key-sha256` from the provider console or another trusted
out-of-band channel. The command verifies the live key and gives Ansible an
ephemeral pinned `known_hosts` file; never disable host-key verification for a
production run. The GitHub compute rollout workflow requires the same
fingerprint for legacy dispatches and obtains it from signed claims for the
declarative paths.

For a provider whose default route is public, define
`faas_private_address` in provider-owned `host_vars` or inventory variables.
It must be the stable private transport address used for host-to-host
communication; do not copy a provider IP into the manifest or daemon URLs.
The preflight rejects missing addresses, non-Linux/non-x86 hosts, and hosts
without systemd. It also caches the discovered peer map for the subsequent
role-limited bootstrap.

### Bootstrap targets (issue #911 / ADR-110)

The split-box inventory maps to two Makefile targets:

| Target | Inventory `--limit` | Hosts | When |
|---|---|---|---|
| `make bootstrap-control-plane` | `control_plane` | fsn-1 (control-plane) | split-box provisioning (PG-1) |
| `make bootstrap-compute` | `compute_nodes` | fsn-2 (compute-only) | split-box provisioning (PG-1) |

For a machine that was created outside the project (GCP, Hetzner, OVH, or
another bare-metal provider), use the provider-neutral adoption pipeline:

```text
gregalectl deploy join-node --manifest-file /secure/manifest.yaml \
  --node fsn-3 --ssh-host 203.0.113.27 \
  --ssh-host-key-sha256 SHA256:<verified-host-key-fingerprint> \
  --artifact-dir /secure/release-join-artifacts --yes
```

The standard release artifact directory must include the
`runtime-bases.env` asset generated by release CI. `join-node` validates its
seven digest-pinned runtime references (including the plain-app `minimal`
base) against the signed production manifest
and stages it as `/etc/faas/runtime-bases.env` before either compute daemon
is enabled.

For a multi-box join, include the same secret-backed `storage.env` for every
box with `--storage-env /secure/storage.env`. The join pipeline installs it
on the control plane and the adopted compute node as `root:faas 0440` and
rejects `FAAS_STORAGE_BACKEND=local` or a `snap/` local-prefix override at
both the CLI and Ansible staging boundaries. The file must set
`FAAS_STORAGE_BACKEND=oci` and `FAAS_OCI_REGISTRY`; credentials
remain outside the repository. This lets vmmd preposition snapshots into
each node's bounded read-through cache without provider-specific disk or
peer-address configuration.

It generates an ephemeral manifest inventory, runs preflight, converges the
compute role, installs the signed release while drained, applies the manifest,
and lets the controller verify and activate the database row only after
readiness. The provider-specific
boundary is only the SSH connection; see
`docs/runbooks/provider-neutral-node-join.md` for the complete contract.

There is intentionally no combined `[box]` group. A host must belong to
exactly one production role group, and `role_convergence` verifies that the
host variable, inventory group, systemd role drop-ins, and active service
set agree. The image builder uses
`deploy/packer/inventory/image-seed.ini` only while baking a role-agnostic
image; it is not a production inventory.

For a split-box fleet, derive the inventory and per-host variables from
the manifest instead of editing committed IPs:

```
make manifest-ansible MANIFEST=deploy/manifest/splitbox.yaml
make ANSIBLE_INVENTORY=deploy/ansible/.generated/inventory/hosts.ini bootstrap-compute
```

The generated `host_vars` owns `faas_box_role`, the canonical
`faas_node_name` (for example `fsn-2.faas`),
`ansible_host`, and `faas_vmmd_target_url`. The generated private endpoint
records written by the overlay role live in each host's `host_vars` for small
fleets and in shared `inventory/group_vars/all.yml` for larger fleets.
`faas_vmmd_target_url` is a node-specific
stable private endpoint such as `tcp://fsn-3.gregale.dev:50051`; the host's
vmmd server leaf carries that endpoint as an additional SAN while retaining
`vmmd.faas` as its role identity. The bootstrap's discovery play gathers one
private address per host. It maps every stable fleet name and keeps the
role aliases (`vmmd.faas` and `egress.faas`) only on the first compute host,
so `/etc/hosts` never resolves a shared alias ambiguously. Providers
whose default route is public set `faas_private_address` in provider-owned
inventory; daemon URLs and the committed manifest remain unchanged. The committed
`host_vars/faas-fsn-{1,2}.yml` files remain checked-in reference fixtures;
the manifest-generated tree is the deployment source of truth.

The generated compute variables also set `faas_gateway_target_url` to the
node's private `gatewayd-internal` listener (`tcp://<node>:8080`). The control
plane's `gatewayd-public` uses `compute_nodes.gateway_target_url` with database
discovery and keeps its API route on `127.0.0.1:8081`; adding or draining a
compute node therefore does not require changing a static first-node target.

### Bootstrap the deployment runner

The provider-neutral GitHub Actions `cd-compute` workflow runs on a trusted
repository-scoped runner labelled `faas-fleet`. Bootstrap it once on the
control-plane host, or target a dedicated management host through a
`fleet_runners` inventory group:

```sh
make ANSIBLE_INVENTORY=deploy/ansible/inventory/hosts.ini bootstrap-fleet-runner
```

The Make target obtains a short-lived repository registration token through an
authenticated `gh` installation when `FAAS_RUNNER_REGISTRATION_TOKEN` is not
already set. It passes that token to Ansible through the controller
environment only.

The role pins and verifies the upstream runner archive, installs its native
dependencies, registers the `faas-fleet` label, and starts an idempotent
systemd service under a non-root account. The token is read only from the
controller environment; it is never stored in inventory or source control.
The workflow's hosted preflight then checks the runner and required production
secrets before a deployment job is allowed to queue.

The fast-root contract is provider-neutral. Set
`fleet.hosts[].storage_device` to an absolute stable device path such as a
provider's `/dev/disk/by-id/...` name, or pass `--storage-device` to
`deploy join-node`. The `xfs` role accepts an existing XFS filesystem or,
only when `--format-storage` is explicitly supplied, a blank block device.
The `xfs` role never selects a disk by size or formats an unknown device. Every
production compute host must end with a dedicated XFS mount at
`storage.fast_root`, with `reflink=1` and `prjquota`; a root-filesystem
fallback is rejected.

The preceding `storage` role provides the provider-neutral automatic path.
When no explicit device is declared, it selects exactly one blank non-root
disk or two equally-sized blank non-root disks for a mirror. It still refuses
mounted, signed, partitioned, or ambiguous devices. An explicitly declared
blank device remains protected by the `--format-storage` /
`faas_storage_format` consent gate; an existing XFS device is reused.

### Compute artifact admission

A production compute node is not allowed to use host-local copies of the
artifacts that define a VM's execution environment. Its root-only
`/etc/faas/storage.env` must contain:

```text
FAAS_STORAGE_BACKEND=oci
FAAS_STORAGE_LOCAL_PREFIXES=none
FAAS_REQUIRE_SHARED_ARTIFACTS=1
FAAS_STORAGE_CACHE_SERVE_STALE=0
FAAS_OCI_REGISTRY=https://<registry>/<organization>
```

These policy lines are enforced by the provider-neutral join boundary, the
`compute_admission` and `compute_only_service` roles, and by the Go storage
backend at daemon startup. `none` is deliberate: leaving the variable unset
retains the legacy local `snap/`, `base/`, `kernel/`, and `layers/` routes and
is unsafe when more than one compute node can restore a deployment. The role
also requires a real deployment manifest before it accepts a compute host's
Firecracker and kernel artifacts; their SHA-256 values must match
`release.firecracker_digest` and `release.kernel_digest`.
This makes a mismatched host fail during join, while the node is still
drained, instead of failing its first customer restore.
Strict mode also requires an HTTPS registry and rejects stale-cache fallback;
the cache may still accelerate successful remote reads, but it cannot serve a
last-known-good blob after the registry reports an error.

For production compute joins, the signed release tarball also carries the
release-pinned `vmlinux`. `node_join.yml` extracts it before importing the
bootstrap playbook; the `firecracker` role copies those exact bytes into
`/srv/fc/base` and `/srv/fc/kernel` and refuses to rebuild a host-local
kernel. This keeps the kernel digest identical across GCP, Hetzner, OVH, and
other bare-metal providers. The local source build remains only for
image-seed/dev bootstrap, with deterministic build metadata.

The release workflow also publishes that same file through the configured
shared storage backend at `kernel/<firecracker_version>` before the node join
starts. `gregalectl artifact publish` is immutable and idempotent: it refuses
to overwrite an existing object whose SHA-256 differs from the signed
`release.kernel_digest`. `node_join.yml` runs `gregalectl artifact verify` with
the staged `storage.env` and manifest before bootstrap/service restart, which
both catches a missing remote artifact early and prewarms the node cache.
This is the required path for remote-only OCI mode; local `/srv/fc/kernel` is
still staged for the host contract but is not used as a hidden split-box
fallback.

The database DSNs remain in the separate root-only
`/etc/faas/compute-db.env`; the shared storage contract and registry
credentials live in `roles/_shared/files/storage.env.example` and remain
operator-supplied. Never commit populated secrets to inventory.

For a split-box manifest, the generated control-plane variables also
declare the database listener address and the compute `/32` allow-list.
Provide `faas_postgres_password` through Ansible Vault (or another secret
source) before bootstrapping; the role refuses to expose PostgreSQL without
that password. The manifest's `postgresql.dsn` must use the same control-plane
mesh address, not `127.0.0.1` or a local Unix socket.

## Do NOT run this on a non-bare-metal host without reading this section

The XFS `prjquota` requirement and the LVM `lv-system`/`lv-fc`
naming come from the reference host's `installimage` recipe (the
financial model ties the snapshot budget to a 2×512 GB RAID-1 layout).
The `lvm` role defaults to `faas_storage_layout=auto`: hosts with the
reference LVM volumes are validated, while provider-native disks such as
GCP persistent disks use their filesystem directly. Set
`faas_storage_layout=reference-lvm` when a fleet requires the reference
layout. The `xfs` role now fails closed when `/srv/fc` is not a dedicated
XFS mount; attach the provider's data disk and declare its stable device path
instead of allowing a root-filesystem fallback.

## After the reference node hosts the executor

Wire `self-hosted, kvm` label to the runner and the existing
`.github/workflows/ci.yml` `metal` job flips on automatically — its
`if: false` guard only stops it running on stock GitHub runners, not
when the right hardware is registered.

Verify end-to-end:

```
sudo make test-metal   # boots a hello-Firecracker VM via the pinned kernel + busybox
sudo make leakcheck    # asserts zero leaked netns / taps / jails / cgroups
```
