# Native end-to-end CI

`e2e-native.yml` is the hardware gate for the platform itself. It runs the
whole `./cmd/e2e` package with the `metal` build tag on `faas-compute-node-2`
in `europe-west3-c`, against real `/dev/kvm` and Firecracker.

It is the companion to [`builder-native-ci.md`](builder-native-ci.md).
`builder-native.yml` proves the builder image and the `pkg/fcvm` package work
on metal; this workflow proves the product does — customer source upload →
`apid` → `builderd` → builder microVM → OCI image → `imaged` → snapshot →
park → gateway wake → invoke, plus the §11 jail fences (`memory.max`, seccomp).

It runs nightly at 04:43 UTC and can be dispatched manually from `main`. The
04:43 slot sits after `builder-native.yml`'s 03:17 nightly; both share the
`builder-native-compute-node-2` concurrency group and the
`/var/lock/faas-builder-acceptance.lock` host lock, so an overrun waits instead
of colliding.

Compute node 2 is the HDD correctness host. The runner exports
`FAAS_TEST_REFERENCE_SSD=0`: wake latency is logged as a diagnostic and is
never enforced here nor counted toward the reference-SSD p95 cohort.

## One-time cloud prerequisite

**Done on 2026-09-12.** The `gregale-builder-native` provider condition matches
`job_workflow_ref` by exact equality, one clause per admitted workflow, so a new
workflow file is rejected until it is named. It now reads:

```
assertion.repository == 'poyrazK/faas' && assertion.ref == 'refs/heads/main' &&
  (assertion.job_workflow_ref == 'poyrazK/faas/.github/workflows/builder-native.yml@refs/heads/main' ||
   assertion.job_workflow_ref == 'poyrazK/faas/.github/workflows/e2e-native.yml@refs/heads/main')
```

**Any future workflow that needs compute node 2 must be added to that
disjunction**, or it fails at the auth step. The node is never contacted, so a
rejected run is inert rather than disruptive. Read the live condition before
changing it — `update-oidc` replaces it wholesale, and dropping the
`builder-native.yml` clause would silently disable that gate too:

```sh
gcloud iam workload-identity-pools providers describe gregale-builder-native \
  --project=project-5ae37259-04cf-4070-bef --location=global \
  --workload-identity-pool=github-actions \
  --format='value(attributeCondition)'
```

No other IAM change was needed: the service account bindings, the instance-level
`roles/compute.osAdminLogin`, and the `gregaleBuilderNodeLifecycle` lifecycle
role already cover this workflow.

## Host prerequisites

The runner refuses to touch a host that is missing any of these, and names the
repair in the failure. Nothing is provisioned implicitly.

| Requirement | Why |
| --- | --- |
| `/etc/faas/builder-acceptance-host` | Designates the node for disruptive tests. Removing it disables this gate before it changes any service state. |
| No active Firecracker process, clean `make leakcheck` | The suite boots its own microVMs and asserts zero leaks afterwards. |
| `/srv/fc/base/vmlinux-6.1.134` (or `FAAS_TEST_KERNEL`) | Guest kernel for every cold boot. |
| `/srv/fc/base/builder-base.ext4` (or `FAAS_BUILDER_BASE_PATH`) | drive0 for the builder microVM. `faas-imaged` stages it on startup via `EnsureBaseExt4`. |
| `br-tenants` up, `net.ipv4.ip_forward=1` | Per-instance veth host side enslaves to the tenant bridge. |
| A reachable Postgres cluster | See below. |

### Postgres is a hard requirement, deliberately

`cmd/e2e` is database-backed, and `pgtest.Open` calls `t.Skip` — not `t.Fatal` —
when `DATABASE_URL` is unreachable. A node without Postgres would therefore
produce a **green run of nearly zero tests**. The runner refuses that outcome:
it probes the cluster with `pg_isready`, then proves the role can `CREATE
SCHEMA` and install `citext` into `public` (both of which `pgtest` needs per
test), and dies with the provisioning commands if either fails. It also refuses
to run at all when `FAAS_SKIP_PG_TESTS` is set.

The DSN is host-owned, not passed down from CI — a DSN on the `gcloud compute
ssh` command line would be visible in the node's process list. Put it in a
root-owned `0600` file:

```sh
sudo -u postgres createuser --createdb faas
sudo -u postgres createdb -O faas faas_e2e
printf 'FAAS_E2E_DATABASE_URL=%s\n' 'postgres:///faas_e2e?host=/run/postgresql&user=faas' \
  | sudo install -m 0600 -o root -g root /dev/stdin /etc/faas/e2e-acceptance.env
```

The runner checks that file's ownership and mode before sourcing it. Point it
at a test cluster or a dedicated database — every test isolates itself into its
own schema, but the gate should not share a cluster with production rows.

## What the run does on the node

The remote command runs as a transient systemd unit, so losing the GitHub SSH
connection cannot kill cleanup halfway through with production daemons still
stopped. `scripts/ci/run-native-e2e.sh` then:

1. takes `/var/lock/faas-builder-acceptance.lock`, pre-flights every fixture
   above, and runs a leak check before touching anything;
2. builds the exact commit's `guest/init` and points `FAAS_GUEST_INIT` at it;
3. stops the local Gregale daemons that are actually active — `apid`, `schedd`,
   `vmmd`, `builderd`, `imaged`, both gateways, `realtimed`, `outboundd` —
   recording each one. `vmmd`, jailer, cgroups, netns and the tenant IP leases
   are host-global; a production daemon left running would fight the test VMs
   and make the closing leak check meaningless. Postgres is never stopped;
4. runs `make PKGS=./cmd/e2e/... RUN_ARGS='-timeout=75m -v' test-metal`;
5. restarts every service it stopped, removes staging, and runs a final leak
   check — on every exit path, including a failed or interrupted run.

## What makes the green check mean something

Two mechanisms, because the tally alone is not enough:

- **Zero executed is a failure.** The log reports passed/skipped/failed and
  lists every skip with the fixture it wanted. A run that executes nothing
  fails instead of looking dormant — the failure mode of the old self-hosted
  `metal` job, which wanted a runner label no runner carried and was cancelled
  or failed 100 dispatches in a row.
- **A required-test contract.** `TestDeployWakeMetal`,
  `TestSourceDeployWakeMetal`, `TestBuildMetal`, `TestWakeTimelineMetal`,
  `TestDeployHealthcheckMetal`, `TestCatalogRuntimeParityMetal`,
  `TestSec11_MemoryMaxFenceEnforced_CrossProcess` and
  `TestSec11_SeccompFilterEnforced_CrossProcess` must actually execute. If any
  of them *skips*, the gate fails and names it. A tally cannot distinguish "the
  suite grew" from "the build path stopped running"; this can.

There is no `-run` filter, and `scripts/ci/run-native-e2e_test.sh` — wired into
the `checks` job in `ci.yml`, so it runs on every PR — fails if one is added,
if the required-test list shrinks, if a required test name stops existing in
`cmd/e2e`, if the Postgres hard-fail is removed, or if the wrapper stops
restoring services.

## Disabling it

Remove `/etc/faas/builder-acceptance-host` from the instance to stop execution
immediately while preserving the identity configuration. Reverting the
workload-identity provider condition revokes GitHub authentication before SSH.
