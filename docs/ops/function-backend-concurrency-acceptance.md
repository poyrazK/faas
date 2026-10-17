# Function backend concurrency acceptance

This gate completes the shared-worker implementation in PR #4768 with
request-scoped cancellation and PostgreSQL integration (ADR-1540). The setting
remains opt-in and takes effect after redeployment. This guide does not enable
production traffic or certify Vercel performance parity.

## Portable checks

Install Go 1.26.9, Node 22+ and Python 3.12+. Run:

```sh
go test -race ./guest/runners/... ./pkg/rootfs -count=1 -timeout=3m
```

The cleanup barrier tests drive the generated adapters in real interpreters.
They prove out-of-order responses, per-request stream routing, cancellation
after async cleanup, Fetch signal/body cancellation, occupied-slot accounting
and worker recycle behavior. They do not require KVM.

For real SQL integration, supply a **disposable** PostgreSQL cluster with
TLS, SCRAM password authentication and a test administrator allowed to create
databases and roles. Put its public CA in `NODE_EXTRA_CA_CERTS`. Supply
`DATABASE_URL` privately through the environment; do not record credentials in
logs or a committed configuration file. Then run:

```sh
FAAS_TEST_FUNCTION_POSTGRES=1 go test -race ./guest/runners/internal \
  -run '^TestConcurrentFunctionPostgres$' -count=1 -v -timeout=2m
```

The test creates a private database and a restricted SELECT/INSERT role, copies
the PostgreSQL starter's database adapter and vendored runtime, installs its
locked `pg` dependency with lifecycle scripts disabled, generates the
production function adapter, and starts the actual HTTP runner. All four phases must
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
It does not qualify function pools spread across several workers. VM-wide
function pool draining and post-response `waitUntil` accounting remain separate
targets. Native acceptance of this revision is pending; this workspace has
no `/dev/kvm`.
