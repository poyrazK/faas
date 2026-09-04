# Provider-neutral compute-node join

`gregalectl deploy join-node` adopts an already-created Linux machine into a
manifest-declared `compute-only` fleet. It does not create VMs, call a cloud
API, edit the repository, or require a provider-specific deployment module.

The provider handoff is deliberately small:

1. Create the machine manually at GCP, Hetzner, OVH, or another bare-metal
   provider.
2. Give the operator an SSH address, user, port, and key that can reach it.
3. Ensure the machine can reach the existing fleet over the private transport
   names declared by the manifest.
4. Run the join command from a runner that can reach the current fleet. The
   existing control-plane host is the simplest runner when the operator laptop
   cannot resolve the private names.

Everything after that boundary is owned by the pipeline.

## Lifecycle

The command performs these phases in order:

1. Validate the manifest and require the requested host to be
   `compute-only`.
2. Generate an ephemeral Ansible inventory from the manifest. The generated
   files are removed at the end and never committed.
3. Override only the new host's Ansible connection target with `--ssh-host`.
   Runtime daemon endpoints remain the stable manifest names.
4. Verify the provider SSH host key against `--ssh-host-key-sha256` and pin
   the verified key in an ephemeral `known_hosts` file.
5. Run the complete-fleet Ansible preflight, unless
   `--skip-fleet-preflight` is explicitly supplied after a recent successful
   preflight.
6. Stage the root-only database environment, a compute trust bundle, image-signing keys,
   signed release assets, bootstrap `gregalectl`, and the manifest.
7. Publish the release-pinned Firecracker kernel through the configured shared
   storage backend at `kernel/<firecracker_version>`. Publication is
   immutable and idempotent: an existing object is accepted only when its
   SHA-256 equals `release.kernel_digest`.
8. Converge the fast-root storage contract. The target must be a dedicated XFS
   mount with `reflink=1` and `prjquota`; a blank device is formatted only when
   `--format-storage` explicitly approves the supplied absolute device path.
9. Register the embedded signed release manifest in `release_bundles` and
   require its topology hash to match the supplied manifest.
10. Run the production `deploy/ansible/bootstrap.yml` compute role.
11. Install the signed release with `--defer-activation`; the database row is
   kept drained while the box is being prepared.
12. Render the manifest, initialize host-local identity, unseal any supplied
   encrypted backup envelopes, and run a node-scoped doctor. Mandatory
    readiness errors always fail the join. Backup envelopes are optional join
    inputs; when they are deferred, the doctor keeps the placeholder warnings
    visible but permits compute-node activation. Supplying the backup pair
    enables the strict warning gate, so a production join that includes backup
    material cannot silently activate with degraded backup posture.
13. Start the four compute services and wait for vmmd's socket, the internal
   gateway, and systemd-active status.
14. Verify the control-plane row's role, release, manifest hash, and stable
   target URL, then activate the compute row only after all gates pass.

If a phase fails, the node remains non-schedulable. Re-running the same command
is the recovery path after correcting the failed input or host condition.

## Required inputs

The release workflow must provide these local artifacts:

- `release.tar.gz`, alongside `release.cosign.bundle` and `release.sbom.json`;
- a Linux/amd64 bootstrap `gregalectl` binary;
- the `cosign` verifier binary;
- either a compute trust-bundle directory containing `ca/ca.crt` and the
  compute-only leaves, or the operator-side full PKI root. If the full root
  is supplied, the pipeline issues/refreshes endpoint SANs locally and still
  never copies `ca/ca.key` to a compute host;
- the image-signing private/public key pair;
- a root-only `compute-db.env` containing non-empty `DATABASE_URL=...` and
  `FAAS_VMMD_DBURL=...` entries for the same PostgreSQL DSN.

For a production compute node, also provide the storage contract through the
manifest or `--storage-device`. If backup is part of the node's responsibility,
the standard artifact directory may contain `box-age-key`, `rclone.conf.age`,
and `archive-creds.json.age`; the latter two are encrypted envelopes and are
unsealed on the host with the staged box identity. Plaintext backup secrets are
never passed as Ansible variables.

The PKI and signing files are sensitive deployment material. Keep them in the
operator's secret store and use a short-lived workspace for the command. The
pipeline stages them over Ansible with restrictive modes; it does not print
their contents.

The standard `cd-compute` workflow performs the kernel publication before
running `deploy join-node`. It uses the same `COMPUTE_STORAGE_ENV` contract
that is installed on the node, so the registry namespace is selected by the
operator's storage configuration rather than by GCP, Hetzner, OVH, or a
provider-specific script. `node_join.yml` repeats the verification and
prewarms the node's read-through cache before the compute daemons can start.

Overlay-specific credentials remain Ansible inputs. For example, a Tailscale
fleet supplies its vaulted/authenticated overlay variables with
`--ansible-vars-file`; a static private network needs no extra overlay secret.

## GitHub Actions automation

The repository includes a manual `cd-compute` workflow for operators who want
the same join path from GitHub Actions. It runs only on a trusted self-hosted
Linux runner labelled `faas-fleet`; the runner must resolve the manifest's
private names, reach PostgreSQL, and SSH to the existing fleet. A control-plane
host is a valid runner when it is dedicated to deployment work. A GitHub-hosted
runner is not sufficient because it cannot reach the private control-plane
database and mesh.

Bootstrap the runner once with `deploy/ansible/fleet_runner.yml` or the
`bootstrap-fleet-runner` Make target. The role pins the runner archive, creates
its non-root system account, installs the runner's native dependencies, and
starts the `faas-github-runner.service` systemd unit. The workflow needs
`curl`, `jq`, GNU `tar`/`base64`/`sha256sum`, and `ansible-playbook`; it installs
its pinned Go-based `cosign` verifier itself.

For new production nodes, prefer the signed bundle inputs. Store the exact
`FleetEnrollmentBundle` YAML and detached cosign signature in private,
immutable HTTPS storage, then dispatch with:

```text
release_tag=v0.1.18-rc.10
node=fsn-4
fleet_bundle_url=https://private-config.example/fleet/production-7.yaml
fleet_bundle_signature_url=https://private-config.example/fleet/production-7.cosign.bundle
fleet_bundle_sha256=sha256:<64 lowercase hex>
```

Set the corresponding `FLEET_BUNDLE_AUTH_TOKEN` production-environment secret.
The bundle publisher must use the pinned keyless GitHub workflow identity; the
repository does not store live node claims or their signatures. The hosted
preflight verifies the digest, signature, expiry/nonce, SSH fingerprint, and
membership in the signed release's production manifest. The self-hosted job
downloads the same bytes again and passes them to `gregalectl deploy join-node`;
the runner's durable `/var/lib/faas-runner/fleet-enrollment-used` ledger makes
successful authorizations single-use.

The workflow first runs a GitHub-hosted preflight. It validates the requested
node inputs, checks the required production secrets without printing their
values, and confirms that an online runner carries the `faas-fleet` label.
This turns a missing runner or missing secret into an immediate actionable
failure instead of leaving the deployment queued indefinitely.

Configure these secrets on the `production` environment. The workflow stages
them only in its short-lived runner workspace and never prints their values:

| Secret | Contents |
|---|---|
| `COMPUTE_SSH_KEY` | private key that reaches the adopted host; the runner's SSH configuration must reach existing fleet peers |
| `COMPUTE_DATABASE_URL` | PostgreSQL DSN used to write both database entries in the node's root-only `compute-db.env` |
| `COMPUTE_STORAGE_ENV` | OCI storage contract (`FAAS_STORAGE_BACKEND=oci` and registry) |
| `COMPUTE_PKI_TARBALL_B64` | base64 of a tar.gz containing `pki/ca/ca.crt` and compute leaves |
| `COMPUTE_SIGN_KEY` / `COMPUTE_VERIFY_KEY` | image-signing key pair |

`COMPUTE_ANSIBLE_VARS_B64` is optional for provider/overlay variables. The
backup secrets are also optional while backup initialization is deferred:
`COMPUTE_BOX_AGE_KEY`, `COMPUTE_RCLONE_ENVELOPE_B64`, and
`COMPUTE_ARCHIVE_ENVELOPE_B64` supply the same encrypted envelopes accepted by
the CLI artifact directory.

The migration dispatch uses a declarative `ComputeNodeClaim` committed in the
selected release source. It contains the manifest node name, the provider SSH
address, and the optional storage policy. Validate one locally with:

```text
gregalectl deploy claim validate --file deploy/claims/compute-node.example.yaml
```

Then dispatch `cd-compute` with only the signed release tag and claim path:

```text
release_tag=v0.1.18-rc.10
claim_file=deploy/claims/compute-node.example.yaml
```

The workflow validates and normalizes the claim on its GitHub-hosted
preflight runner, then passes the result to the trusted `faas-fleet` runner.
The provider address is a connection-only override; the signed manifest
remains the source of truth for runtime DNS, certificates, and the database
target. The previous `node`, `ssh_host`, and storage inputs remain supported
for migration. `format_storage` is intentionally explicit and should only be
enabled for a confirmed blank device.

## Example

Run a plan first:

```text
gregalectl deploy join-node \
  --manifest-file /secure/fleet/production-manifest.yaml \
  --node fsn-3 \
  --ssh-host 203.0.113.27 \
  --ssh-host-key-sha256 SHA256:<verified-host-key-fingerprint> \
  --dry-run
```

Apply from a fleet-reachable runner:

```text
gregalectl deploy join-node \
  --manifest-file /secure/fleet/manifest.yaml \
  --node fsn-3 \
  --ssh-host 203.0.113.27 \
  --ssh-host-key-sha256 SHA256:<verified-host-key-fingerprint> \
  --ssh-user root \
  --ssh-key /secure/ssh/faas-fleet \
  --artifact-dir /secure/fleet/join-artifacts \
  --ansible-vars-file /secure/fleet/overlay-vars.yml \
  --yes
```

If the manifest does not already declare the host's device, add it to this
invocation explicitly. Use `--format-storage` only for a confirmed blank
device:

```text
  --storage-device /dev/disk/by-id/provider-stable-data-disk \
  --format-storage
```

The standard artifact directory contains `release.tar.gz`, its two release
sidecars, `gregalectl-linux-amd64`, `cosign-linux-amd64`, `pki/`, `sign.key`,
`sign-pub.pem`, and `compute-db.env`. It may additionally contain
`box-age-key`, `rclone.conf.age`, and `archive-creds.json.age` for a complete
backup-ready join. Individual flags override those conventions when an
artifact is stored elsewhere.

By default the release SHA comes from `manifest.release.git_sha`. During a
rolling release transition, an operator may pass `--release-git-sha` to adopt
the signed bundle supplied in the artifact directory while the topology
manifest still names the previously installed release. The host installer
verifies that the tarball's embedded release SHA matches this override; the
manifest topology hash remains the durable configuration identity.

The join state is durable in `node_join_jobs`. An interrupted or failed join
can be retried with `--resume`; a lease prevents two operators from changing
the same node concurrently. The command records preflight, convergence,
verification, active, and failed phases and refreshes its lease while Ansible
runs.

The `fleet.hosts[].address` value is not replaced with the provider's public
SSH address. It remains the stable private runtime endpoint and certificate
identity. `--ssh-host` is a connection override for this one adoption run.

## Fast repeated provisioning

When adding more than one machine, prepare the public release assets and the
secret-backed join directory once. `prepare-node` downloads the large release
assets once per tag, verifies `SHA256SUMS` and the keyless release signature,
checks that the supplied topology manifest is the exact signed release
manifest, and reuses the verified files from the operator's persistent cache
on later runs. It refreshes the small checksum and release-manifest metadata on
each run so a mutable tag fails closed.

The secret directory is never uploaded as a repository file. It contains:

```text
compute-ssh-key
compute-db.env
storage.env
sign.key
sign-pub.pem
pki/ca/ca.crt
pki/<compute trust-bundle files>
```

It may also contain `ansible-vars.yml`, `box-age-key`,
`rclone.conf.age`, and `archive-creds.json.age`. The trust-bundle form is
intentional: `prepare-node` refuses to copy `pki/ca/ca.key` into the reusable
artifact directory.

Prepare one claim or a provider-generated batch handoff:

```sh
gregalectl deploy prepare-node \
  --claim-file /secure/fleet/fsn-3.yaml \
  --manifest-file /secure/fleet/production-manifest.yaml \
  --release-tag v0.1.18-rc.15 \
  --secrets-dir /secure/fleet/secrets \
  --output-dir /secure/fleet/join-artifacts \
  --cosign-binary /secure/tools/cosign-linux-amd64
```

The command prints a ready-to-run `join-fleet` command and writes a
normalized `nodes.yaml` into the prepared directory. For later nodes, use
`--nodes-file` with the same artifact directory; release downloads and local
verification are then cache hits. The join command emits phase timings for
local preparation, fleet preflight, control-plane convergence, node
convergence, and activation verification.

## Scale-out operation

The manifest remains the desired fleet topology. Add the new stable private
hostname, runtime port, and (when needed) stable device-by-id storage path to
the manifest, prepare its PKI material, and run one join command. No `host_vars`
file, `hosts.ini` entry, provider API call, or per-cloud code is required. At
larger fleet sizes, run a complete preflight once and use
`--skip-fleet-preflight` only when the fleet facts are still current; the join
itself remains limited to the new node.

For a batch, put only provider connection details in a short-lived file and
let the shared manifest/artifact directory supply everything else:

```yaml
nodes:
  - node: fsn-3
    ssh_host: 203.0.113.27
  - node: fsn-4
    ssh_host: 198.51.100.44
    ssh_port: 2222
```

Run `gregalectl deploy join-fleet --nodes-file nodes.yaml
--manifest-file /secure/fleet/manifest.yaml --artifact-dir
/secure/fleet/join-artifacts --max-parallel 8 --yes`. It runs one complete
preflight, then converges at most eight nodes at a time. Each node still has
its own durable job and lease, so a partial batch can be resumed safely.

If a partially-converged host must be taken out of service first, run
`gregalectl deploy rollback-node --node fsn-3 --yes`. This drains the
control-plane row and records `rolled_back`; it intentionally leaves remote
artifacts untouched for diagnosis. Use the original join command with
`--resume` after correcting the cause.
