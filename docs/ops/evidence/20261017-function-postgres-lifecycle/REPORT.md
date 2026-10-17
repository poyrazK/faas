# Automatic Node function PostgreSQL lifecycle — evidence

Date: 2026-10-17. Branch: `codex/function-postgres-lifecycle`, based on
`c16418b10a40d2b69a75e6d23ce8fd3cfcd492dd`. Scope:
[ADR-1348](../../../adr/1660-function-postgres-resource-lifecycle.md). This
report is committed with the implementation; Git history identifies the revision.
PR #4768 remains closed and unmerged and its merge automation remains paused.
No deployment, production activation or performance-parity claim is made.

## Implemented behavior

- Only trusted qualified runner wiring supplies the versioned worker argument.
  Its generated adapter installs the registry before application import. Ordinary
  production adapters have no bridge. Standalone HTTP/manual hooks remain usable.
- Resource lifecycle version 1 is negotiated separately from background completion.
  Qualified prewarm and request dispatch use one pool identity. Missing/unknown
  capabilities and unmarked legacy adapters fail before handler dispatch; their
  pool identity cannot satisfy a qualified request. Old artifacts need rebuilding.
- Every SDK pool registers before driver creation; the shared starter helper is
  generated from the canonical SDK source. At most 16 owners per interpreter
  bound callback fan-out independently of connection limits and plan quotas.
  Timed-out close retains registration until actual drainage; failed close fails
  checkpoint. Driver/registry errors remain sanitized.
- Default lifecycle endpoints need no application handler plumbing. Explicit
  custom callbacks remain configured. Before-checkpoint consumes the custom
  response stream/cleanup before pool drainage. After-restore rebuilds every pool
  and passes read-only probes before calling the custom callback. All resource
  outcomes settle before success or failure is reported.
- All-interpreter batches and VM execution reservations retain ownership through
  cleanup. Qualified thaw forces resource reconnection. Terminal capture requires
  before-checkpoint; warm and migration captures fail closed even when idle,
  pending a continuing-VM recovery protocol. Old artifacts need rebuilding.
- The strict native target now includes deployed real SQL, a pre-capture drainage
  witness, restore/thaw and worker/runner death. This is test source until executed
  on the supported native acceptance host.

## Portable validation

Toolchains: Go 1.26.9 linux/amd64, Node 24.19.0/22.23.3 and Python 3.12.14. Scoped Go
commands use `GOMEMLIMIT=3GiB GOGC=25 GOMAXPROCS=2`, `-p 1`, the shared build
cache, local listener access and `umask 022`. Initial parallel builds exhausted
memory/storage; their compilers and derived temporary/cache artifacts were
removed. Bounded compilation avoids that contention. Source and Git history were kept.

The canonical SDK build/test build, generated-helper drift check and 14 focused
tests passed, including packed-runtime import/typecheck without `pg` or the
control-plane SDK. SDK cases cover registration before factory effects, capacity
refusal sanitization, deferred close ownership, cancellation, restore probes,
late failures and uncertain COMMIT with no SQL replay.

The following Node 24 race suite passed with a fresh disposable TLS/SCRAM SQL
fixture and `FAAS_TEST_FUNCTION_POSTGRES=1`:

```sh
go test -p 1 -race ./guest/runners/... ./pkg/rootfs -count=1 -timeout=3m
```

Node 22.23.3 additionally passed all 13 SDK pool unit cases and
`go test -p 1 -race ./guest/runners/internal -run '^TestFunctionResources'`
with `-count=1 -timeout=3m`, a new disposable TLS/SCRAM fixture and the same
explicit SQL gate. The Node 24 full internal suite took 17.207 seconds;
Node 22's final focused resource suite took 2.748 seconds. These runs include the
old-adapter refusal and qualified-prewarm regression cases. Disposable SQL containers, volumes,
certificates and credentials were removed, with empty label-filtered container
and volume listings verified afterward.

The actual HTTP/H2C runner and generated adapter load four interpreters with two
pools each. Gate-controlled pool ends cannot acknowledge checkpoint early; one
failed pool cannot hide unfinished neighbours. Custom async-iterator and Fetch
ReadableStream bodies finish before drainage, and custom restore callbacks can
query rebuilt pools. Registration capacity is checked before driver creation;
ordinary adapters do not acquire the qualified bound.

`TestFunctionResourcesPostgres` uses the locked shipped `pg` package, an isolated
database and restricted role, verified certificate/hostname TLS, SCRAM and an
independent SQL observer. Three writes remain blocked after 202 responses in
three workers; a fourth worker brings the session count to eight. Unlocking
commits exactly three rows. An authenticated test-only resource then holds hook
acknowledgement while all eight actual SQL sessions close. Releasing that peer
allows checkpoint acknowledgement. Restore opens eight fresh TLS sessions with
the same three rows. A real interpreter exit while SQL is blocked must recover
without committing or replaying the interrupted mutation. This test injects the
host transport; it does not establish real vsock or VM recovery evidence.

Focused default imaged, fcvm and scheduler lifecycle/ownership checks passed.
The host accounting and guest-init race checks also passed:

```sh
go test -p 1 -race ./pkg/functionlifetime ./pkg/fcvm/activity ./guest/init \
  -count=1 -timeout=3m
go test -p 1 -race ./pkg/fcvm ./pkg/sched \
  -run '^Test(Function(Resource|Execution)|ThawWithRunsResumeHookWhenAsked)' \
  -count=1 -timeout=3m
```

The dedicated acceptance build passed focused manifest, ownership, capture and
thaw unit tests with fake VM boundaries:

```sh
go test -p 1 -tags metal,function_background_native \
  ./pkg/functionlifetime ./pkg/imaged ./pkg/fcvm ./pkg/sched \
  -run '^Test(Function(Resource|Execution)|ApplyAppLifecycle|ThawWithRunsResumeHookWhenAsked)' \
  -count=1 -timeout=3m
```

The native e2e test package compiled with `metal,function_background_native`.
Fresh static guest-init, Node 22 and Node 24 runner binaries also built with those
tags. Compilation and fake VM tests do not constitute native execution evidence.

These local `CGO_ENABLED=0` acceptance builds have the following SHA-256 hashes:

| Artifact | SHA-256 |
| --- | --- |
| guest-init | `560bdd984535413afbc46d148af36493649d7dc9b18962ff42e21ca2cdd7d8b6` |
| Node 22 runner | `a5173423b2fdf7a8e2e4951d033bd180821eef968de636d352fdd5f391d7f58f` |
| Node 24 runner | `60c5a0de881e272bffd3c9b04b779026439d4347ad89d901470db76ca794b627` |

Scoped default golangci-lint passed with zero findings across the affected runner,
worker pool, generated adapter, manifest, VM manager, scheduler and SQL fixture
packages. Native-tagged changed-code lint also passed with zero findings.
The SQL observers and native HTTP requests inherit the test deadline; lock
cleanup retains a separately bounded context after test cancellation. The final
Node 22 real SQL rerun passed after this context propagation correction.

Workflow actionlint, ADR uniqueness, retained security-rule baseline, runbook SQL,
text encoding, shell quoting, sealed-environment scope and documentation links
passed. The egress Go/Jinja cross-check and bridge-name guard passed using the
installed system Python with Jinja2 3.1.6; the default agent Python lacks Jinja2.
The existing gate skipped live nft validation because `nft` is not on PATH. This
does not qualify native kernel rules. No GitHub CI result or PR merge is claimed.

## Native qualification boundary

The new strict SQL phase is `TestFunctionResourcesPostgresMetal` in
`make test-function-background-native`. It requires a source-built qualified
guest-init/Node 22 runner/daemons, designated isolated native x86_64 Linux host,
root, KVM, current kernel/builder base, disposable catalog and SQL service, and
an isolated SQL proxy interface with a matching public CA. See the
[procedure](../../function-backend-concurrency-acceptance.md).

Its test-only control resource holds checkpoint acknowledgement while independent
SQL and proxy observers require zero sessions, before capture can finish or VM
destruction can close sockets. It then requires real snapshot restore, all eight
sessions recreated, generation advancement relative to each interpreter's own
baseline, in-place thaw after proxy disconnection, worker replacement and runner
generation retirement without replaying interrupted SQL. Instrumentation adds a
third registered test resource per interpreter; it contains no SQL/DSN/password.

This workspace has no root or `/dev/kvm` and cannot qualify native execution or
native leak checks. `make test-function-background-native` was attempted and
fails its strict root prerequisite. `make leakcheck` was attempted and fails
closed because `/dev/loop0` through `/dev/loop7` cannot be inspected. These are
prerequisite failures, not qualification. Build-tagged unit tests and test
compilation are portable
source checks only. Node 24 native execution, lost/duplicate acknowledgements over
real vsock, watchdog/uncertain cleanup, vmmd restart/recovery and private
environment qualification remain outstanding. Production stays off; durable
execution and exactly-once database semantics are not claimed.

The repository's existing strict metal command was also run:

```sh
FAAS_REQUIRE_FUNCTION_CONCURRENCY_METAL=1 \
  RUN_REGEX='^TestFunctionConcurrencyMetal$' make test-metal PKGS=./cmd/e2e
```

Its helper/test/daemon builds completed. Execution failed with
`function qualification requires root on native x86_64 Linux`; no phase was
silently treated as passing or skipped native qualification. The new SQL native
phase remains unexecuted on supported hardware.
