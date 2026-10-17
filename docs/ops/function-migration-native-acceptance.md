# Function migration, replies and coordinator restart on native hosts

Status: harness implemented; native execution pending; production off.
This is the successful-migration, reply-boundary and coordinator process-loss
slice of [ADR-1577](../adr/1577-function-migration-destination.md) and
[ADR-1578](../adr/1578-function-migration-scheduler-restart.md), for Node 22 and Node 24. The complete
[destination qualification matrix](function-backend-concurrency-acceptance.md#measured-migration-destination-qualification)
still applies before release.

## Prepared isolated fixture

Use two dedicated physical x86_64 Linux KVM hosts with root SSH access, verified
known host keys, cgroups v2, the normal jailer/network/storage configuration and
otherwise empty tenant residency. Containers, nested virtualization, two database
rows or two local daemon processes cannot qualify this gate. The coordinator
runs as root and may be the control-plane member of this isolated fixture; it
forwards each VM owner's Unix RPC socket over SSH.

Prepare one disposable migrated platform catalog named
`faas_function_migration_native_<fixture UUID without dashes>`. Both hosts' node
registrations and all prepared platform services must use that catalog. Retain
the default-local node row required by the scheduler constructor, and ensure only
the two physical fixture hosts are eligible for placement. Configure shared
capture storage, current producer/registry approvals, measured application
admission, native process recovery/publication and authenticated VM RPC handling
according to the existing native fixture procedure. This harness installs no
services, creates no node registrations and changes no production configuration.

Run `faas-vmmd.service` and `faas-schedd.service` on each host from the same clean
committed revision being tested. Build their binaries, guest-init and both Node
runners using the coordinator's Go toolchain and these exact flags:

```sh
CGO_ENABLED=0 go build -trimpath -buildvcs=false -tags metal,function_background_native -o /private/staging/vmmd ./cmd/vmmd
CGO_ENABLED=0 go build -trimpath -buildvcs=false -tags metal,function_background_native -o /private/staging/schedd ./cmd/schedd
CGO_ENABLED=0 go build -trimpath -buildvcs=false -tags metal,function_background_native -o /private/staging/guest-init ./guest/init
CGO_ENABLED=0 go build -trimpath -buildvcs=false -tags metal,function_background_native -o /private/staging/node22 ./guest/runners/node22
CGO_ENABLED=0 go build -trimpath -buildvcs=false -tags metal,function_background_native -o /private/staging/node24 ./guest/runners/node24
```

Stage these assets on both hosts. The prepared image service must use the same
fresh guest-init and runner bytes when producing the new function artifacts;
checking an unused staged runner is insufficient. The test compares immutable
rootfs/base producer digests with the fresh builds and the native receipt's actual
consumed drives. Stale runtime-default bases must be rebuilt and approved through
their existing producer flow. Preserve the qualification build boundary: these
binaries are for isolated acceptance only.

Also stage the pinned kernel, builder base, Firecracker binary and the checkout's
`deploy/scripts/leakcheck.sh` with its sibling `leakcheck_native_loops.py` on both
hosts. Their hashes must match the coordinator's selected files. The live daemon
executables are hashed through `/proc`, so replacing an on-disk binary without
restarting its isolated service does not pass.

On each host, prepare `/etc/faas/function-migration-acceptance-host` as a
root-owned regular JSON file, with no group/other write permission. It binds the
actual host node to this fixture and exact 40-character committed revision:

```json
{
  "fixture_id": "11111111-1111-4111-8111-111111111111",
  "revision": "<exact committed revision>",
  "catalog": "faas_function_migration_native_11111111111141118111111111111111",
  "node_id": "<this host's registered node UUID>"
}
```

Provide a disposable TLS-enabled PostgreSQL cluster with SCRAM password storage.
The catalog connection role needs CREATEDB/CREATEROLE and permission to inspect
SCRAM verifiers in `pg_authid` (normally the disposable cluster's fixture
superuser). It creates isolated customer SQL databases and restricted roles.
The coordinator's explicitly selected public
IPv4 interface must be reachable from guests for the raw TLS proxy and the
authenticated lifecycle witness. The SQL certificate must cover that interface's
IP address and the catalog's hostname; guests use `verify-full` and the supplied
public CA. Keep SQL credentials in the coordinator environment, never in the
fixture JSON or committed evidence. No production database or customer account
belongs in this fixture.

Prepare API and gateway loopback HTTP forwards on the coordinator. The API must
use the disposable catalog: a fresh catalog-only random key must authenticate a
read before the test performs any API mutation. The gateway must route the
fixture app domain. Normal API, image, build, scheduler and gateway services must
already be healthy. Background reconcilers remain enabled; the test invokes the
real scheduler migration coordinator without starting another scheduler loop.

## Fixture input and execution

Keep this non-secret JSON file outside the checkout. Replace every illustrative
host, node, domain, IP and path with the prepared fixture's values. `sql_ip` must
be a real guest-reachable interface, rather than an arbitrary address. Both host
asset maps have exactly the same keys:

```json
{
  "version": 1,
  "fixture_id": "11111111-1111-4111-8111-111111111111",
  "catalog": "faas_function_migration_native_11111111111141118111111111111111",
  "api_url": "http://127.0.0.1:18080",
  "gateway_url": "http://127.0.0.1:18081",
  "app_domain": "apps.fixture.example",
  "sql_ip": "<coordinator public IPv4>",
  "sql_ca_path": "/private/fixture/ca.pem",
  "fc_version": "<staged Firecracker version>",
  "hosts": [
    {
      "node_id": "22222222-2222-4222-8222-222222222222",
      "ssh": "root@native-source",
      "remote_socket": "/run/faas/vmmd.sock",
      "assets": {
        "vmmd": "/private/staging/vmmd",
        "schedd": "/private/staging/schedd",
        "guest_init": "/private/staging/guest-init",
        "node22": "/private/staging/node22",
        "node24": "/private/staging/node24",
        "kernel": "/private/staging/vmlinux",
        "builder_base": "/private/staging/builder-base.ext4",
        "firecracker": "/private/staging/firecracker",
        "leakcheck": "/private/staging/leakcheck.sh",
        "leakcheck_native_loops": "/private/staging/leakcheck_native_loops.py"
      }
    },
    {
      "node_id": "33333333-3333-4333-8333-333333333333",
      "ssh": "root@native-destination",
      "remote_socket": "/run/faas/vmmd.sock",
      "assets": {
        "vmmd": "/private/staging/vmmd",
        "schedd": "/private/staging/schedd",
        "guest_init": "/private/staging/guest-init",
        "node22": "/private/staging/node22",
        "node24": "/private/staging/node24",
        "kernel": "/private/staging/vmlinux",
        "builder_base": "/private/staging/builder-base.ext4",
        "firecracker": "/private/staging/firecracker",
        "leakcheck": "/private/staging/leakcheck.sh",
        "leakcheck_native_loops": "/private/staging/leakcheck_native_loops.py"
      }
    }
  ]
}
```

Set `DATABASE_URL` privately to the exact catalog, with `sslmode=verify-full`
and its trusted CA. Set `FAAS_TEST_KERNEL`, `FAAS_BUILDER_BASE_PATH`,
`FAAS_TEST_FIRECRACKER` and `FAAS_TEST_FC_VERSION` to the coordinator's staged
assets/version. Do not set `FAAS_SKIP_PG_TESTS`. Then run:

```sh
export FAAS_FUNCTION_MIGRATION_NATIVE_CONFIG=/private/fixture/config.json
export FAAS_FUNCTION_MIGRATION_NATIVE_EVIDENCE=/private/evidence/new-run
export FAAS_FUNCTION_MIGRATION_NATIVE_CONFIRM=isolated-two-hosts
make test-function-migration-native
```

The output directory must be new and outside the clean committed checkout. The
driver builds fresh qualified assets, compares running host binaries and staged
hashes, verifies distinct physical machine/boot identities and runs both native
leak checks before mutations. It opens private temporary SSH socket forwards and
compiles the complete native test executable with the race detector. Only
`TestFunctionMigrationDestinationPostgresMetal` runs. Each runtime must execute
six separately deployed cases: `success`, `lost-readiness-reply`,
`lost-commit-reply`, `duplicate-adoption`, `scheduler-restart-before-commit` and
`scheduler-restart-after-commit`. The timeout is 180 minutes for the complete
twelve-case matrix. Fixture input remains version 1; output reports use version 3.

For each runtime the test observes four interpreter PIDs, two TLS pools per
interpreter and three independently committed SQL rows. Checkpoint ACKs stay held
until all eight SQL/proxy sessions close. Destination recovery ACKs stay held until
all eight fresh SQL backend identities are visible; every interpreter's two pool
generations must increase exactly once. A separate observer holds the completed
readiness RPC before the scheduler receives it. At that point the catalog must
still name the source and a direct destination execution request must explicitly
refuse service. This barrier establishes ordering; it does not simulate real
packet loss.

The scheduler then commits and activates the exact measured destination. The
normal gateway must serve it with current configuration and the original SQL
rows. Retirement requires removal of the source migration lease, absence from
the source VM owner's inventory and an SSH witness showing the original
Firecracker process generation and named network namespace are gone. PID, UID
and netns strings may coincide on different physical hosts; identity includes
the host. Both runtime apps are deleted through the API and destination physical
retirement is verified before qualification. Final leak checks run on both hosts.

## Controlled reply-boundary cases

Each fault case starts with its own app, instance and migration attempt. The
readiness observer retains the actual request/reply privately and the scheduler
waits at that boundary. Only protobuf fingerprints enter the report; sealed
application inputs never do.

- `lost-readiness-reply`: after the remote vmmd completes readiness, discard the
  client reply and explicitly retransmit the identical request once. Forward the
  actual retry response to the waiting coordinator. Require the same grant,
  receipt, Firecracker process generation, namespace and eight SQL backends.
- `lost-commit-reply`: run the real PostgreSQL ownership transaction, then return
  a controlled error through the test store adapter. The existing scheduler
  resolver must read the durable committed decision, activate that exact owner
  and retire the source. This case does not synthesize a successful decision.
- `duplicate-adoption`: send three concurrent independent copies of the original
  adoption request to the remote vmmd before allowing ownership commit. Every
  response must match, and execution must remain closed on that ready owner.

The authenticated lifecycle peer keeps per-interpreter checkpoint/recovery call
counts after barrier release. Every worker must perform exactly one checkpoint
and one recovery during this migration attempt, with no extra callback through
the retries or activation. SQL backend identities must remain unchanged across
the adoption retransmissions; every case must retain the original committed rows
and pass the physical retirement/leak checks.

These are deterministic after-effect errors at client/store boundaries. They do
not cut a network socket, interrupt PostgreSQL COMMIT on the wire or drop guest
vsock packets. The fixture's explicit adoption retry tests vmmd idempotency;
production scheduling still rolls back adoption errors. An automatic adoption
retry policy is outside this increment. Host/vmmd restart and actual transport
loss remain separate qualification work.

## Coordinator process loss and startup recovery

For `scheduler-restart-before-commit` and `scheduler-restart-after-commit`, the
fixture starts a separate child of the freshly compiled native test executable.
That process owns the production migration coordinator, its PostgreSQL pool,
ledger and VM RPC clients. Its event pipe identifies the completed readiness
boundary; only the post-commit case then releases readiness. A named-field store
adapter delegates the real ownership transaction and holds its completed reply
before activation or source acknowledgement.

The parent sends SIGKILL to its own child and verifies the actual signal exit.
No cancellation return or deferred migration rollback substitutes for process
loss. The catalog must retain the exact ready or committed epoch and the source
cleanup lease. In the uncommitted case it must still name the source and retain
the pending destination claim. Direct destination execution must remain closed;
SSH must still witness the original paused source process.

A replacement child receives only its role and destination node, without the
instance, lease, wake or original AppSpec. It opens a new catalog pool and RPC
clients, discovers the attempt from PostgreSQL, runs
`Engine.RecoverFunctionMigrations`, then `Engine.SeedLedger`. Real schedd startup
uses this same ordering before admission begins. Discovery must identify the
expected phase and finish exactly one attempt. The parent observes both process
IDs/start generations and executable hashes through procfs; the replacement must
use the same qualified binary in a distinct process.

The destination must retain its original Firecracker generation, receipt and
eight TLS SQL sessions. Callback counters must stay unchanged from readiness,
all original SQL rows must remain, and source retirement and final native cleanup
must pass. Rejection tests and portable subprocess tests qualify fixture wiring
only. A separate real-PostgreSQL test checks that a new pool/store cannot spend
pending destination capacity before confirmed retirement.

These cases kill the fixture migration coordinator; prepared systemd schedd and
vmmd services remain running. They do not qualify an actual service restart,
startup after a host reboot, vmmd process recovery, HA or database failover.
Issued restores and expired/stale ready attempts remain on the existing fenced
expiry path rather than being restarted or granted a new wake.

## Evidence and remaining gates

`tests.raw`, `tests.jsonl`, `tests.stderr`, `build.log`, `migration-recovery.json` and
`verdict.json` stay in the private evidence directory. Private
`coordinator-*.log` files retain each fixture child's output for failure diagnosis.
Reports include source and
binary hashes, host/daemon identities, receipt hashes, SQL backend generations,
worker pool generations, callback counts, request/reply fingerprints and observed cleanup. They contain no SQL DSN, private
key, secret value or sealed payload. Any failed/skipped/missing runtime, changed
source/daemon, incomplete witness or failed cleanup prevents a successful verdict.
The driver removes only its own tunnels and temporary build directory.

If physical ownership is uncertain, the test retains its fixture account and
durable migration records for diagnosis. Customer SQL fixture credentials and
databases are still revoked/removed. Inspect the named native fixture hosts and
resolve quarantine through the existing owner/recovery procedures before retrying;
do not delete migration history to force a green run. No retry is automatic.

A successful run has report version 3 and scope
`native-migration-coordinator-restart-v1`. The driver sets
`reply_boundaries_qualified` and `coordinator_restart_qualified` only after all
twelve cases and both final host leak checks pass. Old baseline-only or reply-only
reports cannot qualify this scope. Reports always
have `failure_matrix_qualified: false` and `production_ready: false`. Real lost ACKs,
failed probes, cancellation/expiry/commit races, native capacity pressure,
systemd schedd service restart, source/destination vmmd or host death/restart and
private-environment authority remain future native
cases. No native result has been obtained from the managed development workspace.
