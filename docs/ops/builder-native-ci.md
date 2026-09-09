# Native builder CI

`builder-native.yml` is the post-merge hardware gate for the Gregale builder
and the native `pkg/fcvm` package.
It starts after a successful `images` workflow on `main` when that run published
`builder-base`, runs nightly, and can be dispatched manually from `main`.
Non-runtime `images` runs are skipped. Nightly and manual runs select the most
recent successful `builder-base` publish. The job checks out the exact commit
that published `ghcr.io/poyrazk/builder-base:sha-<commit>` and tests its amd64
child on `faas-compute-node-2` in `europe-west3-c`.

The workflow uses GitHub OIDC through this keyless GCP identity:

- workload identity pool: `github-actions`
- provider: `gregale-builder-native`
- service account: `gregale-builder-ci@project-5ae37259-04cf-4070-bef.iam.gserviceaccount.com`

The provider condition admits only `poyrazK/faas`, `refs/heads/main`, and
`.github/workflows/builder-native.yml@refs/heads/main`. The service account has
project-level `roles/compute.viewer`, instance-level
`roles/compute.osAdminLogin` on compute node 2, and the custom
`gregaleBuilderNodeLifecycle` role. Its lifecycle binding is conditioned on
the exact resource name for `faas-compute-node-2`, and contains only
`compute.instances.get`, `compute.instances.start`, and
`compute.instances.stop`. It can act as the service account attached to that
VM so OS Login can establish the SSH session. It has no service-account key.

The workflow starts the target instance when it is off, waits for SSH, and
stops it after the native jobs finish. If the instance was already running,
the workflow leaves it running. This keeps the test node off between CI runs
without creating a manual prerequisite for nightly CI.

The target instance has instance-level `enable-oslogin=TRUE` metadata and must
contain `/etc/faas/builder-acceptance-host`. Removing that marker disables the
test before it changes service state. The runner also refuses a host with an
active Firecracker process or any resource detected by `make leakcheck`.

The nightly and manually dispatched workflow also transfers the exact current
`main` source plus the pinned Go toolchain, creates fresh hello base/layer ext4
fixtures, and runs the whole metal-tagged `pkg/fcvm` package. Tests that need
fixtures the workflow does not yet stage skip with their names in the log; a
run that executes zero tests fails. The output reports passed, skipped, and
failed totals so the remaining fixture gap is visible. The package exercises
the production jailer, Firecracker, TAP, network namespace, cgroup, readiness,
destroy, and leak-check paths on amd64. The metal job waits for builder
acceptance and both jobs serialize through
`/var/lock/faas-builder-acceptance.lock`.

The remote command runs as a transient systemd service. Losing the GitHub SSH
connection therefore does not kill cleanup halfway through. The root wrapper
serializes runs with `/var/lock/faas-builder-acceptance.lock`, stages the exact
builder image below `/srv/fc/acceptance`, temporarily stops active Gregale
services, runs the full Dockerfile and Railpack suite, restores the previous
service state, removes staging, and performs another leak check. The production
builder at `/srv/fc/base/runner-builder-amd64.ext4` is never replaced.

Each successful fixture is converted to its deployable ext4 form and scanned
before its cold boot. Scanner errors fail acceptance. The admission threshold
matches image publishing: a CRITICAL or HIGH finding with an available fix
fails the workflow. Unfixed findings remain visible in the uploaded acceptance
log. This is a release-fixture policy; customer deployment scans remain the
informational contract described in ADR-075.

Inspect the cloud-side trust and host designation with:

```sh
gcloud iam workload-identity-pools providers describe gregale-builder-native \
  --project=project-5ae37259-04cf-4070-bef --location=global \
  --workload-identity-pool=github-actions
gcloud compute instances describe faas-compute-node-2 \
  --project=project-5ae37259-04cf-4070-bef --zone=europe-west3-c \
  --format='value(metadata.items.enable-oslogin)'
gcloud compute ssh faas-compute-node-2 \
  --project=project-5ae37259-04cf-4070-bef --zone=europe-west3-c \
  --command='cat /etc/faas/builder-acceptance-host'
```

To disable execution immediately while preserving the identity configuration,
remove `/etc/faas/builder-acceptance-host` from the instance. Disabling the
workload identity provider revokes GitHub authentication before SSH.
