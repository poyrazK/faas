# Function backend concurrency acceptance

This guide covers shared-worker cancellation and PostgreSQL integration
(ADR-1540), the Node background-completion foundation (ADR-1571), and the
acknowledged VM execution lifetime/checkpoint barrier (ADR-1572) and automatic
Node PostgreSQL ownership (ADR-1573).
Shared-worker concurrency is opt-in and takes effect after redeployment.
The new background-completion contract is disabled in production runners.
This guide does not enable production traffic or certify Vercel performance parity.

## Portable checks

Install Go 1.26.9, Node 22+ and Python 3.12+. Run:

```sh
go test -race ./guest/runners/... ./pkg/rootfs -count=1 -timeout=3m
go test -race ./pkg/functionlifetime ./pkg/fcvm/activity ./guest/init -count=1 -timeout=3m
go test -race ./pkg/fcvm ./pkg/sched -run '^TestFunctionExecution' -count=1 -timeout=3m
```

The cleanup barrier tests drive the generated adapters in real interpreters.
They prove out-of-order responses, per-request stream routing, cancellation
after async cleanup, Fetch signal/body cancellation, occupied-slot accounting
and worker recycle behavior. They do not require KVM.

The background tests also exercise the actual runner and H2C listener with
generated Node adapters. Held promises prove response EOF over HTTP/1 and HTTP/2
before execution completes, including event, streaming and Fetch handlers.
Nested work, rejection, per-plan aggregate admission across interpreters,
structural task caps, timeout and disconnect cleanup, uncooperative-worker
retirement, single-slot negotiation and recycle retention must pass. Cancellation
keeps task admission until cleanup or worker retirement, and a completed HTTP
request is not replayed after worker death. These tests inject trusted runtime
wiring; there is no production environment/manifest opt-in. They require local
socket access as well as interpreter subprocesses. Shortened package-internal
deadline fixtures do not change public plan limits.

For real SQL integration, supply a **disposable** PostgreSQL cluster with
TLS, SCRAM password authentication and a test administrator allowed to create
databases and roles. Put its public CA in `NODE_EXTRA_CA_CERTS`. Supply
`DATABASE_URL` privately through the environment; do not record credentials in
logs or a committed configuration file. Then run:

```sh
FAAS_TEST_FUNCTION_POSTGRES=1 go test -race ./guest/runners/internal \
  -run '^TestConcurrentFunctionPostgres$' -count=1 -v -timeout=2m
FAAS_TEST_FUNCTION_POSTGRES=1 go test -race ./pkg/fcvm \
  -run '^TestFunctionExecutionPostgres$' -count=1 -v -timeout=2m
```

The test creates a private database and a restricted SELECT/INSERT role, installs
the starter's locked `pg` dependency with lifecycle scripts disabled, generates
the production adapter, and starts the actual HTTP runner. All four phases must
pass:

1. Two SQL requests blocked by an independent table lock exhaust admission;
   a third receives `database_busy`. Cancelling one frees local admission,
   a neighbour still queries in the same Node process, and after unlocking
   only the healthy write exists.
2. Public hook impersonation fails, a trusted checkpoint callback closes that
   worker's sessions, admission stays closed until a fresh pool passes its
   read probe, and its SQL backend PID changes without changing the Node PID.
3. Independent termination of idle SQL sessions followed by the restore/thaw
   callback creates a usable pool with a new generation.
4. Killing the worker fails its in-flight query, subsequent requests use a new
   process, concurrent invocation IDs and headers stay isolated, and the write
   count stays unchanged.

A client disconnect does not synchronously interrupt PostgreSQL's lock wait.
The test witnesses local admission cleanup first, then verifies rollback after
the lock releases. Connection counts and admission bounds apply per interpreter;
server-side disconnected sessions can persist until the statement timeout.
The lifecycle callbacks in this process test are simulated guest-init calls.
It does not exercise real snapshot capture, frozen thaw, credential delivery or
hook broadcast across multiple interpreter processes.

The second SQL test separately combines the real generated adapter with the
host Manager's execution ledger and a replaced VM boundary. A restricted
SCRAM role attempts an INSERT over verified TLS behind an independently held
table lock. Its HTTP response frame must arrive while SQL remains blocked;
park/freeze must refuse, and Stats must remain occupied. Releasing the lock
must produce one row, a successful task settlement and execution completion.
Duplicate completion must not decrement twice. This test does not exercise
native Firecracker, credential delivery or an actual native SQL restore.

## Supported native acceptance

Use a designated, isolated native x86_64 Linux KVM acceptance host, as required
by `CLAUDE.md`. Build and stage the guest-init, Go/Node/Python runners, CLI,
kernel and builder base from the reviewed revision. Provide the harness's
disposable PostgreSQL fixture. Set:

```text
DATABASE_URL
FAAS_TEST_KERNEL
FAAS_BUILDER_BASE_PATH
FAAS_GUEST_INIT
FAAS_E2E_FUNCTION_RUNNER_GO124
FAAS_E2E_FUNCTION_RUNNER_NODE22
FAAS_E2E_FUNCTION_RUNNER_PYTHON312
FAAS_E2E_GREGALE_BIN
```

Keep a clean checkout, serialize use of the host's acceptance lock, and run
leak checks before and after, including after a failed suite:

```sh
make leakcheck
FAAS_REQUIRE_FUNCTION_CONCURRENCY_METAL=1 \
  RUN_REGEX='^TestFunctionConcurrencyMetal$' make test-metal PKGS=./cmd/e2e
make leakcheck
```

Strict mode fails if KVM, the designated host marker, native host detection,
SQL, an interpreter runner or CLI fixture is missing. Ordinary development
mode can still skip unavailable fixtures; a skipped phase is not qualification.
Retain `go test` output and artifact hashes for the reviewed source revision.
The binary, Node, Python async, Python sync and manifest phases must all pass.

Each runtime phase requires eight two-second requests to finish in under
3.5 seconds in one interpreter, then proves an abandoned request leaves that
worker serving past the five-second cancellation deadline. The suite records
batch throughput, eight-sample p95 and interpreter RSS high-water where reported.
These are small acceptance samples, not production capacity estimates; RSS
excludes guest/kernel/runner overhead and p95 is the largest of eight samples.

Run the [PostgreSQL native gate](managed-postgres-native-acceptance.md)
separately for the shipped HTTP starter's real checkpoint/restore/thaw evidence.
It does not qualify function pools spread across several workers. The new
acknowledged execution lifetime/barrier implementation has portable evidence;
its native acceptance remains pending.

## Native function execution lifetime gate

Use the same designated, isolated native host, clean reviewed checkout,
acceptance lock, disposable catalog, kernel and builder base. Run:

```sh
make leakcheck
make test-function-background-native
make leakcheck
```

This target builds guest-init and the Node 22 runner from source with
`metal,function_background_native`. The harness propagates that tag to its
daemons and refuses a prebuilt daemon directory. The test fails on missing
root, KVM, host designation, native-host detection, staged artifacts, build
capabilities or enabled SQL; it cannot qualify through a skipped phase.
Retain output and hashes of all kernel/base/guest/runner/daemon artifacts with
the source revision, and run leakcheck after a failure too.

`TestFunctionBackgroundMetal` holds work after HTTP completion in three
different interpreters, verifies host occupancy, and requires freeze, warm
capture and scheduler park/reuse refusal. A fourth interpreter releases the
file witness. The gate then requires exactly one mutation per ID, idle freeze
and thaw callbacks in all four interpreters, a forced terminal capture and a
real restore with every loaded interpreter visited. File witnesses are not
PostgreSQL pool-draining evidence.

`TestFunctionResourcesPostgresMetal` adds an actual deployed Node 22 SQL function
using two SDK pools per interpreter and no application lifecycle endpoints. The
same strict target requires this phase too. Prepare the disposable TLS/SCRAM SQL
service as in the [native SQL procedure](managed-postgres-native-acceptance.md),
then set `GREGALE_POSTGRES_NATIVE_SQL_IP` to its isolated acceptance proxy IPv4
address and `GREGALE_POSTGRES_NATIVE_CA_PATH` to its public CA file. Its certificate
must include that IP in the SAN. Never use production database credentials.
The test binds a SQL proxy at port 5432 and an authenticated test-only HTTP peer
on a dynamically allocated port on that interface; both are explicit app egress
ports. The control token is sealed as a disposable app secret.

The SQL phase requires three blocked post-response writes in distinct workers,
eight TLS sessions across four workers, refusal of active freeze/park, and no
early checkpoint acknowledgement. A test-only registered resource holds the
callback while independent SQL and proxy observers require zero sessions; pool
drainage must therefore precede capture and VM destruction. After release, a real
snapshot restore must rebuild all eight sessions and retain exactly the committed
rows. In-place thaw after proxy disconnection must replace the pools. Real worker
and runner exits must fence ownership and recover without replaying interrupted
SQL. No skipped phase qualifies. The SQL target requires both Node 22 and 24
runner builds and runs the phase independently for each runtime. It also holds
warm checkpoint and recovery acknowledgement separately: zero SQL/proxy sessions
must precede capture, eight fresh sessions must precede the held recovery ACK,
and the continuing source retains its interpreter IDs with newer pool
generations. Terminal restore and in-place thaw follow this warm cycle. These
are required assertions, not recorded native results.

Before production activation, additionally qualify lost acknowledgements and
duplicate/late events over real vsock, runner/worker death, watchdog deadlines,
vmmd restart/recovery and uncertain cleanup on the native host. Execute the
new SQL phase above to qualify pool draining and worker/runner death; its source
and compilation are not execution evidence. Portable fault injection covers these
accounting cases but cannot replace native evidence. Private environment
qualification restores do not yet expose the new lifetime port. Normal builds
remain disabled; the special build is an acceptance instrument, not a rollout
approval. This workspace has no `/dev/kvm` and cannot certify the native gate.

## Function migration source rollback qualification

[ADR-1575](../adr/1575-function-migration-source-rollback.md) adds a source-only
qualification seam. Production activation stays off. ADR-1576 adds measured source permission in the
native qualification build; destination adoption rejects function-source captures. A native source
fixture must retain the existing boot/admission contract, never disable a guard
or discard an admission receipt to make a migration capture run.

Require the same isolated native host and matching Node 22/24 artifacts. Observe
all eight SQL sessions closing before source pause/publication. Hold the recovery
ACK until eight fresh, probed sessions exist; ordinary requests must stay closed.
Exercise cancellation during checkpoint, pre-Phase-2 expiry, lost capture and
recovery acknowledgements, commit/cancel races, failed probes, caller cancellation,
Destroy joining recovery, and unconfirmed physical cleanup. Repeated cancel/expiry
must not replay callbacks or SQL. The original wake can return to RUNNING only
after recovery; confirmed retirement completes STOPPED. Unconfirmed cleanup keeps
the allocator and scheduler residency/concurrency reservations. Late events must
leave a replacement and a committed peer untouched.

Also qualify vmmd restart with the durable source receipt and no reconstructed
in-memory owner. It must retain uncertainty, not resume a VM using an empty
execution ledger. Private environment resume authority remains mandatory. Passing
portable ownership/transaction tests or compiling the tagged suite does not
qualify these native cases. See the
[local evidence](evidence/20261017-function-migration-source-rollback/REPORT.md).

`TestFunctionMigrationAdmissionPostgresMetal`, included in
`make test-function-background-native`, runs Node 22 and 24 independently for
cancel, expired lease, capture cancellation, failed SQL probe and held recovery
ACK timeout. The suite materializes an application standard before deployment
and requires a retained protocol-v2 measured runtime receipt. It cannot silently
fall back to an unmeasured source. Separate capture/recovery permissions bind
the original wake, lease, physical receipt and immutable capture keys.

An independent peer holds checkpoint until all eight SQL/proxy sessions close.
For successful recovery it holds the final ACK until all four interpreters have
eight fresh, probed sessions. The original wake remains MIGRATING through that
ACK. Duplicate cancellation must preserve the same pool generation; expiry uses
the real scheduler watchdog. A failed probe or incomplete capture must retire
the source, and committed rows remain unchanged. Holding ACK past the host
deadline checks retirement without callback replay; this is a controlled timeout,
not evidence of lost real-vsock packets. The expanded target allows 90 minutes
for its independent deployment fixtures.

Portable permission and transaction tests are selected by `^TestFunctionMigration`
in the backend CI lane. They cover current-input drift, purpose/parent/keys,
atomic registration, expired-lease recovery and permission renewal. Actual KVM,
vsock loss/duplication, restart, private-environment and leak acceptance remain
required. See [ADR-1576 evidence](evidence/20261017-function-migration-admission/REPORT.md).

## Measured migration destination qualification

ADR-1577 adds a dedicated destination restore capability in
`metal,function_background_native` builds. Normal builds continue to refuse it.
The existing strict native target qualifies source cancellation/recovery; it is
not a two-host destination acceptance result. Do not count its source-only cases
as completion of this matrix.

The successful-migration, controlled reply and coordinator process-loss portion
has a guarded entry point,
`make test-function-migration-native`. Follow the
[prepared two-host fixture procedure](function-migration-native-acceptance.md).
Its version 3 verdict requires baseline, readiness/commit reply-discard,
duplicate-adoption and coordinator SIGKILL/replacement cases before and after
ownership commit for both Node runtimes. Startup recovery follows
[ADR-1578](../adr/1578-function-migration-scheduler-restart.md). These tests do not
qualify actual transport loss, systemd service restart or vmmd/host restart. The
complete failure/restart matrix below and production activation remain separate
gates. Local compilation and portable
witness tests supply no native verdict.

Use two designated native x86_64 KVM hosts, their independently built qualified
vmmd/guest/runners, shared disposable capture storage/catalog, current artifact
approvals and a restricted TLS/SCRAM PostgreSQL fixture. Run Node 22 and Node 24
independently. Record original source node/wake and measured receipt, four worker
identities, all eight pool sessions and committed SQL rows before migration.

1. Hold the checkpoint witness until all eight SQL and proxy sessions close.
   Require the durable measured capture result before Phase 2. Hold destination
   recovery ACK until four interpreters have eight fresh, successfully probed
   sessions. While the destination is `issued` or `ready`, require the instance
   row to remain MIGRATING on the source and execution requests to stay closed.
2. Commit the exact destination receipt, wake and network identity through
   schedd. Verify native/configuration receipts change atomically. Activate the
   recorded destination, then acknowledge and confirm original-source retirement.
   Require the original committed rows, healthy SQL reads and new physical
   process/network identity; do not replay the interrupted customer invocation.
3. Inject duplicate adoption and lost readiness/commit/activation ACKs. Require
   one load and one recovery callback attempt per destination. A committed
   attempt must resolve to its exact epoch; another target/wake cannot replace it.
4. Race expiry/cancellation against restore and commit, fail a pool probe, and
   fail physical cleanup. A rollback must fence destination permission and
   confirm destination retirement before recovering the original source. Keep
   allocator and durable capacity ownership while cleanup is uncertain.
5. Restart each scheduler and vmmd before readiness, after readiness and around
   commit. Pending destinations must continue to consume database RAM/CPU/vCPU
   headroom. A vmmd without the original in-memory physical owner must refuse
   fabricated teardown/readiness and retain quarantine. Include private
   environments and current-input/artifact/incarnation drift.
6. Run `make leakcheck` after confirmed retirement, save the complete native
   evidence and only then consider activation. A compiled suite, simulated
   process receipt, held portable callback or successful schema replay supplies
   no native cross-host, vsock or leak verdict.

Portable CI selects `^TestFunctionDestination` in addition to the existing
migration/source suites. Its PostgreSQL ownership and capacity races remain
separate from the native matrix. See [ADR-1577](../adr/1577-function-migration-destination.md).
