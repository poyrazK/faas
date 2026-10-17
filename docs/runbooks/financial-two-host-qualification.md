# Physical two-host cost-control qualification

This lane runs the existing native financial component tests with the second
owner on a different physical host. It also runs shared compute/egress
contention and real SQL connection cuts against each host, followed by owner
death, physical retirement, retained credits and exactly-once settlement.
App and Job attempts contend on the same account book. The losing attempt
must allocate no slot, journal, network or guest.

The full-daemon financial lane runs first on node A: APID, imaged, schedd,
VMMD, both gateways and meterd. Its separate real-meterd replay fixtures retain
canonical provider events and PostgreSQL receipts. Those historical replay
quantities are synthetic; they are not usage collected from the two-host guests.
The pair fixture registers real native owners but prepares price/allowance and
sampling rows directly. API activation is covered by the full-daemon lane.

The full-daemon report also requires canonical response-byte evidence for
`ForwardHTTPStream` (ADR-1364). On the real funded guest it exercises v1,
per-request v2 and persistent v2 handlers, including four concurrent streamed
responses per mode. Those mode checks use an in-process production RPC server
and a live-identity adapter; the actual VMMD daemon then serves the database
outage/quota-exhaustion, private-isolation and owner-restart checks. The adapter
does not create financial authority or native resources. This is node-A
handler/daemon evidence, not a cross-host overlay-traffic claim. Supporting
source hashes and all four bridge binary hashes are mandatory in the report.

Local compilation and portable rejection tests do not qualify either host.
No publication, deployment or remote execution is part of preparing this lane.

## Dedicated fixtures

Use two drained native x86_64 Linux KVM hosts. Both need Firecracker and jailer
with identical hashes, the native networking tools, cgroups v2, loop devices,
`br-tenants`, IPv4 forwarding and the platform's normal tenant host policy.
Native public forwarding also requires nftables 1.1.3+, Linux 6.13+ and the
matching host policy, plus effective loose or
disabled `rp_filter` on the root tenant bridge. Strict reverse-path filtering
is incompatible with its device-specific output route; publication refuses
unsupported routing instead of changing global host configuration.
Neither host may serve customer workloads or maintain a resident warm pool.
Node A also needs the pinned Go toolchain, a static busybox, build tools,
the existing full-system kernel/base assets and PostgreSQL tooling.

Provision `/etc/faas/financial-pair-acceptance-host` on both designated hosts.
Node A also requires the existing `/etc/faas/builder-acceptance-host` marker
for the full-daemon runner. The pair uses the same host flock as the other
native gates. Do not run M9, builder or other fault drills against these hosts
concurrently.

Node A needs noninteractive SSH and sudo access to node B. Its root-owned,
mode `0600` `/etc/faas/financial-pair.env` contains:

```sh
DATABASE_URL='postgresql://fixture-role@fixture-postgres/acceptance?sslmode=verify-full'
FAAS_FINANCIAL_PAIR_SSH_B='acceptance-peer'
FAAS_FINANCIAL_PAIR_ADDR_A='10.0.0.12'
FAAS_PGTEST_TEMPLATE_DATABASE=1
```

Use an exclusively disposable PostgreSQL fixture. The role needs permission to
create pgtest's private clone databases. Pair children refuse a shared database
name. Node B reaches two per-test TCP proxies on node A's configured numeric
fixture IP; restrict access to the acceptance pair with the fixture firewall.
TLS and PostgreSQL authentication remain end to end. Credentials travel in the
SSH stdin configuration, not in process arguments or the peer archive.

The controller builds the native test binary, static VMMD helper, guest init
and two-drive images once. It copies those exact artifacts and the kernel to
node B and checks their hashes, plus both host tools, before starting drills.
Host/boot identities are checked again in each native owner's startup evidence
and after cleanup. Two rows, two containers or two SSH aliases to one host
cannot satisfy the report.

## Execution and CI

From an exact source archive with `.faas-e2e-source-sha` and a matching pinned
toolchain, invoke the guarded controller entry point as root:

```sh
FAAS_E2E_SOURCE_SHA='<reviewed 40-character SHA>' \
FAAS_E2E_RUN_ID='<unique ID, at most 32 characters>' \
FAAS_E2E_GO='<absolute path to pinned go>' \
FAAS_E2E_TRANSFER_ROOT='/var/tmp/faas-native-e2e-<unique ID>' \
bash scripts/ci/run-native-financial-pair.sh
```

`financial-pair-native.yml` stages this archive and toolchain automatically on
a dedicated self-hosted runner labelled `gregale-financial-pair`. It is manual
and restricted to main. Provision the label, host markers, SSH and disposable
SQL fixture before dispatch; the workflow does not create infrastructure.

The existing `e2e-native.yml` also exposes `financial-system`. Full/qualify runs
include that job after the main platform job so both serialize on the same
acceptance host. It has its own time budget. The job collects and verifies
native/provider logs, all four reconciliation vectors and the JSON verdict
before removing host staging, and uploads them on failure as well as success.

## Evidence and cleanup

`financial-pair-evidence/qualification.json` binds the source SHA and run ID to:

- distinct native host/boot identities and matching executables, guest images
  and Firecracker/jailer binaries;
- every source-selected financial component test, all new per-host partition
  and restart phases, and packet/offload cases on both hosts;
- the full-daemon native verdict and independently reconciled raw integer
  provider quantities, receipt vectors and funded invoice bounds;
- actual command statuses, leakcheck logs and service restoration on both hosts.

The SSH hold receives controller heartbeats. A lost controller, timeout or
closed channel triggers owner shutdown, leak inspection and service restoration.
A repaired leak remains a failure. Both hosts keep failed ownership evidence
for repair. Successful runs remove native fixture staging after copying its
evidence. Successful CI runs remove controller staging after artifact upload.
Failed runs retain their logs/source/toolchain for operator repair and removal.

The report's `qualified` field covers physical native financial components
plus the separate full-daemon/provider lane. **`promotion_ready` remains false.**
Before promotion, close these remaining gates:

1. Execute the required `ForwardHTTPStream` response-byte checks and extend
   traffic coverage to the real cross-host gateway/owner route, custom main
   ports, sidecars, H2C/gRPC, upgrades and public TCP/UDP. The ADR-1364 candidate
   routes native-recovery public bridges through the canonical funded interface;
   legacy owners cannot attest strict readiness. Positive ARP counters, source
   inspection and compilation cannot substitute for response-payload evidence.
2. Run the complete strict app/job/worker/sidecar/private workload matrix.
3. Measure the 100-cycle reference-SSD wake p95 against the spec's 350 ms target,
   and set and meet a reviewed funded-egress throughput target. This lane does
   not invent a throughput threshold or treat an HDD host as the reference SSD.
4. Complete external-provider sandbox/catalog reconciliation and the remaining
   native lifecycle release gates.

An accepted component report does not authorize enabling strict budgets in
production while those gates remain open.
