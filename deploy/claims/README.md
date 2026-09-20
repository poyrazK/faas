# Compute node claims

`ComputeNodeClaim` is the provider-neutral handoff between creating a bare
metal machine and adopting it into Gregale. A provider or operator supplies
the connection facts once; the signed release, deployment manifest, PKI,
secrets, Ansible roles, and database lifecycle remain owned by Gregale.

The claim deliberately contains only:

- `metadata.name`: the compute-only node name already declared by the signed
  production manifest;
- `spec.ssh`: the provider's temporary connection address, user, port, and
  (for a production-signed bundle) the expected host-key fingerprint;
- `spec.storage`: an optional stable device path and an explicit format flag.

It must not contain passwords, private keys, database DSNs, certificates, or
runtime daemon addresses. The SSH address is a connection-only override and is
never written into the node's runtime topology.

## Signed enrollment bundles

For a production join, use a signed `FleetEnrollmentBundle` instead of
passing provider facts as workflow inputs. The bundle is a short-lived,
single-use authorization document separate from the application release. It
contains one or more claims, an expiry, a random nonce, and the expected
OpenSSH `SHA256:` host-key fingerprint for every node. The production manifest
still owns roles, stable runtime names, certificates, release identity, and
storage policy.

Keep the bundle and its detached cosign signature in private configuration
storage. This repository is public, so a live bundle must never be committed,
attached to a public GitHub release, or pasted into an issue. The provider
address and host-key fingerprint are operational infrastructure data even
though they are not credentials.

Validate the private artifacts before dispatching:

```sh
gregalectl deploy fleet-bundle validate \
  --file /secure/fleet/fleet-enrollment.yaml \
  --signature /secure/fleet/fleet-enrollment.cosign.bundle \
  --manifest-file /secure/fleet/production-manifest.yaml \
  --json
```

Generate the unsigned document from a provider-produced claim without hand
editing timestamps or nonces:

```sh
gregalectl deploy fleet-bundle create \
  --claim-file /secure/fleet/fsn-4.yaml \
  --manifest-file /secure/fleet/production-manifest.yaml \
  --name production --generation 7 \
  --output /secure/fleet/production-7.yaml
```

The command refuses to overwrite an existing file. A trusted publisher then
signs those exact bytes and uploads the YAML plus detached signature to the
private configuration store; do not rewrite or reformat the YAML after
signing.

The verifier accepts only the pinned keyless identity for the fleet-enrollment
publisher workflow, requires the GitHub OIDC issuer, checks the exact signed
bytes, validates the seven-day maximum lifetime and clock window, confirms
every claim names a manifest-declared `compute-only` host, and requires the
host-key fingerprint. The join command records a consumption marker under
`/var/lib/faas-runner/fleet-enrollment-used` after activation; retries of the
same bundle/node are rejected while failed joins remain resumable.

Validate a claim before using it:

```sh
gregalectl deploy claim validate \
  --file deploy/claims/compute-node.example.yaml
```

When a signed production manifest is available, also verify that the claim
names a declared `compute-only` host:

```sh
gregalectl deploy claim validate \
  --file /secure/fsn-4.yaml \
  --manifest-file /secure/production-manifest.yaml \
  --json
```

## GitHub Actions

The preferred `cd-compute` dispatch references private, immutable bundle
artifacts. The workflow downloads both files over HTTPS, checks the supplied
SHA-256 digest, validates the detached signature and production-manifest
membership on its hosted preflight, then downloads and verifies them again on
the trusted `faas-fleet` runner:

```text
release_tag=v0.1.18-rc.10
node=fsn-4                         # omit when the bundle has one claim
fleet_bundle_url=https://private-config.example/fleet/production-7.yaml
fleet_bundle_signature_url=https://private-config.example/fleet/production-7.cosign.bundle
fleet_bundle_sha256=sha256:<64 lowercase hex>
```

The production GCS path authenticates both workflows with short-lived GitHub
OIDC credentials; converge its publisher/reader identities once with
`scripts/ops/gcp_fleet_enrollment_identity.sh --apply`. Do not copy a one-hour
`gcloud auth print-access-token` value into a GitHub secret: a queued fleet
runner can outlive it. Non-GCS private configuration services may instead set
`FLEET_BUNDLE_AUTH_TOKEN` and `FLEET_BUNDLE_PUBLISH_TOKEN` on the `production`
environment. The endpoint must return exact immutable bytes for the digest;
bearer credentials are never printed. The bundle publisher signs with the
workflow identity pinned by the CLI (`.github/workflows/fleet-enrollment.yml`
on `main`) and publishes the bundle and signature atomically before dispatch.
A provider adapter only needs to create the machine and produce the claim; it
does not need a GCP, Hetzner, or OVH deployment module.

For migration, `cd-compute` still accepts `claim_file` as a release-source
input. The path is resolved from the selected release source, validated on a
GitHub-hosted preflight runner, and its normalized values are passed to the
trusted `faas-fleet` runner. This reduces the dispatch to:

```text
release_tag=v0.1.18-rc.10
claim_file=deploy/claims/fsn-4.yaml
```

The claim must be present in the selected release source. This keeps the
claim and the signed release auditable as one immutable deployment request.
The legacy `node`, `ssh_host`, and storage inputs remain available while
existing operators migrate.

## Direct CLI

For a bounded batch, `deploy join-fleet` can consume a single claim directly:

```sh
gregalectl deploy join-fleet \
  --claim-file /secure/fsn-4.yaml \
  --manifest-file /secure/production-manifest.yaml \
  --artifact-dir /secure/join-artifacts \
  --yes
```

`storage.format: true` is rejected unless `storage.device` is also supplied.
Formatting remains an explicit operator decision; the remote storage role
still refuses unsafe, mounted, or non-blank targets.
