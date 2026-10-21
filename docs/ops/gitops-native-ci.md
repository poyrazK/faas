# GitOps native acceptance CI

The manual [GitOps native workflow](../../.github/workflows/gitops-native.yml)
runs the image API/worker resilience journey and all GitHub/GitLab,
source/Dockerfile scheduled-Job journeys through the existing guarded runner.
It tests the exact dispatch commit on `main`, with no alternate source-ref input,
PR trigger, automatic schedule, deployment or production configuration change.
The reviewed code must land on `main` before this lane can exercise it.

This lane needs an exclusive physical x86_64 Linux KVM host. The internal n2
node provides nested-KVM evidence outside this lane: run the guarded runner
there with `GREGALE_GITOPS_NATIVE_HOST_CLASS=nested`
([ADR-20261021083000](../adr/20261021083000-gitops-nested-kvm-acceptance-evidence.md)).
Do not stop tenant VMs to make a host eligible. A missing dedicated runner leaves
the job queued; an invalid host or incomplete evidence cannot produce a pass.

## Provision the exclusive runner

Use a disposable Actions runner restricted to this repository and trusted
manual workflows. Assign labels `self-hosted`, `linux`, `x64`, and
`gregale-gitops-acceptance`. Configure the `gitops-acceptance` GitHub environment
to allow only `main`, with operator review appropriate for privileged native
tests. The workflow itself also requires `refs/heads/main`.

The runner account needs passwordless `sudo` for the checked-out test wrapper
and Python verifier. This is privileged reviewed code, not a sandbox for
arbitrary PRs. Root uses a per-process `safe.directory` setting for only the
tested checkout. Checkout credentials are not persisted. Go is installed from
`go.mod` by `setup-go`; the wrapper verifies that exact version before building
guest-init from the tested checkout. There is no prebuilt guest-init override.

The host needs the prerequisites in the
[native acceptance guide](../environment-gitops.md#isolated-enforcement-acceptance):
Firecracker/jailer, cgroups v2, the designated-host marker
`/etc/faas/builder-acceptance-host`, required network/ext4 utilities, a qualified
kernel, an empty leak gate, no active Gregale services, and at least 32 GiB free
on checkout and temporary volumes. No host service or tenant VM is stopped by
this lane. It takes the same host flock as the other native acceptance runners;
GitHub concurrency also prevents overlapping GitOps dispatches.

Create `/etc/faas/gitops-native-acceptance.env` as a regular root-owned file with
mode `0600` (no symlink), containing shell-quoted values:

```sh
DATABASE_URL='postgres:///gitops_acceptance?host=/run/postgresql&user=gitops_acceptance'
FAAS_TEST_KERNEL='/srv/fc/base/vmlinux-qualified'
GREGALE_GITOPS_NATIVE_BUILDER_REF='registry.example/builder@sha256:<64 lowercase hex digits>'
GREGALE_GITOPS_NATIVE_SOURCE_RUNTIME_REF='registry.example/node-runtime@sha256:<64 lowercase hex digits>'
GREGALE_GITOPS_NATIVE_DOCKERFILE_RUNTIME_REF='registry.example/minimal-runtime@sha256:<64 lowercase hex digits>'
```

Replace every placeholder with a verified image digest; tags and credential
URLs are rejected. Configure registry access on the isolated host if needed.
The database must be disposable and allow the harness's schema creation and
extensions. Its DSN stays in this host-owned file, never workflow inputs,
command arguments, Actions output or uploaded artifacts. Use acceptance-only
configuration; do not source a production daemon environment.

## Dispatch and inspect

After provisioning, manually dispatch **GitOps native acceptance** on `main`.
The hosted contract job runs the report and CI-verdict tests first. The native
job checks a clean checkout at the dispatch SHA, builds fresh guest-init, and
invokes `scripts/test-environment-gitops-native.sh` with host class `native`.
The tests retain their 300-minute alarm. A 330-minute outer timeout sends TERM,
allows five minutes for cleanup, and leaves time within the 360-minute job for
the always-run verdict and artifact steps. Hard cancellation may interrupt
cleanup; missing post-run evidence still fails, and the next run must pass the
empty-host leak preflight. No cleanup pass is manufactured by deleting VMs.

The verifier independently reads the structured test events, recomputes the v2
verdict, checks every stage and the expected source SHA, and rehashes all five
raw artifacts. Missing/skipped/failed scenarios, diagnostic-only hosts, changed
artifacts, a failed wrapper or inconsistent pass flags prevent CI success.
The Actions acceptance step itself must also have succeeded; an old record
cannot override a failed, cancelled or skipped current invocation.

The job summary and 14-day artifact contain only `summary.json` and `summary.md`:
the dispatch SHA, fixed coverage, per-test outcomes, stage states, safe reason
codes, the v2 report fingerprint and raw artifact fingerprints. Host identity,
tool output, extra report fields and raw logs are not copied into this public
projection. The `gregale.dev/gitops-native-ci/v1` projection identifies its
underlying `gregale.dev/gitops-native-acceptance/v2` evidence contract.

Private evidence remains under
`$RUNNER_TEMP/gregale-gitops-native-<run-id>-<attempt>/private`, with setup logs
and the checkout-built guest-init alongside it. Root retains private modes;
only the two sanitized files are readable by the Actions account. Save private
diagnostics through an authorized operator before recycling the disposable
runner. Do not publish them as Actions artifacts. This lane does not delete
host resources or evidence directories automatically.

A passing result proves the complete local-provider-fixture matrix at that
commit. It does not establish live provider access, protected-review native
qualification, in-flight scheduled-Job crash recovery, or permission to enable
production qualification/enforcement. Those gates remain unchanged.
