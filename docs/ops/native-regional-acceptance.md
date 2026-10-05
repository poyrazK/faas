# Native regional acceptance

Run `make native-regional-acceptance` against a dedicated fleet running the
candidate apid, schedd, vmmd, imaged and builderd with the current migrations.
This is a qualification gate for the shared-control-plane placement contract
in ADR-591, ADR-594 and ADR-586. Independent regional control planes, geographic
ingress and data residency remain separate milestones.

Use a dedicated acceptance API with `regional_compute_enabled = true` and
the selected registry region accepting new definitions, so the tests can
create their fixtures. Keep production regional admission disabled until
qualification passes.

The controller needs Linux x86_64, Go 1.25.13, database access, an acceptance
account API token, and noninteractive SSH/sudo access to three workers. A and
PEER must be the only registered workers in the selected region. B must be an
eligible worker in another region. Each worker must have native Linux x86_64,
KVM, a distinct machine identity, active schedd/vmmd, and the operator-created
marker `/etc/faas/regional-acceptance-host`. VM and container hosts, including
nested KVM hosts, refuse preflight. The controller does not host test VMs.

Drain customer workloads and other acceptance runs first. These tests stop
vmmd on A and PEER, change admission policy and capacity ceilings, and stop
builderd/imaged during source-export handoff. Use only designated acceptance
workers. The Jobs test checks every worker with `leakcheck` before and after
its workload and retains terminal instance placement, not just active rows.

Set the following environment variables using the active inventory and
protected credentials. Keep the token and DSN out of command logs:

| Variable | Value |
| --- | --- |
| `DATABASE_URL` | Candidate fleet's PostgreSQL DSN |
| `FAAS_REGION_API_URL`, `FAAS_REGION_API_TOKEN` | Candidate apid and acceptance account credentials |
| `FAAS_REGION_NODE_A`, `FAAS_REGION_NODE_PEER`, `FAAS_REGION_NODE_B` | Registered compute-node names |
| `FAAS_REGION_SSH_A`, `FAAS_REGION_SSH_PEER`, `FAAS_REGION_SSH_B` | SSH aliases for those same distinct hosts |
| `FAAS_REGION_IMAGE` | Digest-pinned known-good HTTP app image |
| `FAAS_REGION_JOB_IMAGE` | Digest-pinned Linux amd64 OCI image with `/bin/sh`, `sleep`, and `echo` |
| `FAAS_REGION_SOURCE_TARBALL` | Known-good HTTP app source archive for build/export handoff |
| `FAAS_REGION_WORKER_REPO` | Plain absolute candidate checkout path on each worker, for `deploy/scripts/leakcheck.sh` |
| `FAAS_REGION_VMMD_METRICS_URL_B` | B's loopback HTTP metrics URL, e.g. `http://127.0.0.1:9104/metrics` |
| `FAAS_REGION_ACCEPTANCE_CONFIRM` | `native-x86` |

The gate executes the affected metal suite with race detection. It requires
the app cold-wake, saturation, owner-recovery and whole-region-outage drills,
the regional source-build/export handoff, and both Jobs recovery drills to
pass. Skips and missing drills fail the dedicated gate. Ordinary metal runs
continue to skip these fleet-only tests without explicit opt-in.

The Jobs drill observes a real running task, stops its worker, and requires a
retained failed attempt followed by successful execution on the regional peer.
It then stops both regional workers, admits a new run against the existing
definition, and observes queued work for one minute without a new instance or
consumed retry. B's `JobColdBoot` operation counter must not change, including
refused RPCs. Its systemd invocation must also stay unchanged, preventing a
process restart from hiding counter increments. Restoring a regional peer
must execute the queued work with its original retry budget.

Tests restore changed services and remove their own workloads on exit. The
runner also restores A/PEER vmmd, builderd and imaged on failure or interruption.
Inspect failures and cleanup diagnostics before reusing the fleet. Keep the printed private
evidence directory, worker binary hashes/release manifest, image digests,
inventory, candidate commit, complete test output and leak-check results.
Passing portable tests or compiling the metal harness does not qualify the
fleet. Native execution evidence is still required before enabling admission.
