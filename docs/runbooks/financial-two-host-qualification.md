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

The pair lane also selects `TestMetalFinancialPairGatewayProtocols`,
`TestMetalFinancialPairGatewayFailureBounds` and
`TestMetalFinancialPairGatewayOwnerRestart`. The controller accepts real local
HTTP/H2C, Upgrade, TCP and UDP client traffic through the production gateway
transports and SQL-backed `NodeClientCache`. These dial the production VMMD
RPC server on node B over fixture-scoped mutual TLS. That server wraps the
existing real native Manager and its PostgreSQL financial authority; it has no
fake dialer, fake guest or synthetic byte counter. The already-admitted target
is supplied by the fixture: this lane does not duplicate gateway daemon,
API/scheduler admission, certificate-registry or sidecar lifecycle acceptance.

The disposable guest serves a main port at `9000`, auxiliary/sidecar-routing
ports at `8080` and `9001`, gRPC over H2C at `9002`, binary Upgrade echo,
TCP echo at `9090` and UDP echo at `9091`. The same funded attempt serves all
protocol cases without resetting its interface counter. Four concurrent HTTP
streams must complete. During a real SQL connection cut, quota exhaustion must
stop public payload while the guest is alive; a trusted private service-hop
request must complete without increasing public bytes. Quota checks include
explicit zero-payload retries for H2C/gRPC, Upgrade, TCP and UDP
after the HTTP flood exhausts the shared interface quota. Physical retirement
must finish within the funded deadline without refunding uncertain credits.
After owner death, recovery must reject the old target and settle exactly once.
Only an explicit manual resume and a new attempt may use fresh authority.

Every required phase logs `financial-pair-traffic:` JSON. The verifier requires
all 14 phases, distinct attested host/boot identities, the peer TCP address,
native attempt/lease/incarnation and interface identities, actual payload and
canonical counter deltas, issued byte credits, and the shared integer money
ceiling. It rejects counter resets, duplicate measurements, billed private
bytes, retry payload after exhaustion, late physical retirement and reused
authority. The compiled fixture and both host records retain supporting-source
hashes. All bridge binaries, the guest executable and both guest layers are
hashed with the native artifacts. Parent test success and both strict host
leakchecks remain mandatory; repaired leaks never qualify.

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
FAAS_FINANCIAL_PAIR_ADDR_B='10.0.0.13'
FAAS_PGTEST_TEMPLATE_DATABASE=1
```

Use an exclusively disposable PostgreSQL fixture. The role needs permission to
create pgtest's private clone databases. Pair children refuse a shared database
name. Node B reaches two per-test TCP proxies on node A's configured numeric
fixture IP; restrict access to the acceptance pair with the fixture firewall.
TLS and PostgreSQL authentication remain end to end. Credentials travel in the
SSH stdin configuration, not in process arguments or the peer archive.
For traffic tests, node A must also reach ephemeral TCP listeners on node B's
configured numeric fixture address. They require the per-fixture client
certificate. The runner creates this CA in memory and sends only the peer's
leaf identity over stdin; it never uses production CA or node keys. No listener
is bound on an unspecified address and no firewall rules are relaxed by tests.

The controller builds the native test binary, static VMMD and all four bridge
helpers, the multi-protocol guest workload, guest init
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

The report's `qualified` field covers physical native financial components,
cross-host gateway transports and the separate full-daemon/provider lane.
**`promotion_ready` remains false.**
Before promotion, close these remaining gates:

1. Execute the required single-host `ForwardHTTPStream` checks and all 14
   cross-host gateway transport phases with the exact immutable bundle. Extend
   full daemon/API/scheduler admission, registered node-certificate validation
   and sidecar process lifecycle coverage across hosts as well. The ADR-1364
   candidate routes native-recovery public bridges through the canonical funded
   interface; legacy owners cannot attest strict readiness. Positive ARP
   counters, source inspection and compilation cannot substitute for measured
   response-payload evidence. Local preparation supplies no KVM execution proof.
2. Run the complete strict app/job/worker/sidecar/private workload matrix.
3. Measure the 100-cycle reference-SSD wake p95 against the spec's 350 ms target,
   and set and meet a reviewed funded-egress throughput target. This lane does
   not invent a throughput threshold or treat an HDD host as the reference SSD.
4. Complete external-provider sandbox/catalog reconciliation and the remaining
   native lifecycle release gates.

An accepted component report does not authorize enabling strict budgets in
production while those gates remain open.
