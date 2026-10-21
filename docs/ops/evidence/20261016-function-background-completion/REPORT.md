# Node background-completion foundation — portable evidence

Date: 2026-10-16. Branch: `codex/function-background-completion`, based on
`667948df980b4f77ce91d354026a3c95941cb59e`. Scope: ADR-1346's runner/Node adapter
completion protocol. The implementation and this report are committed together;
Git history identifies the reviewed revision. This follow-up depends on the
existing concurrent-worker implementation; PR #4768 is closed and unmerged.

## Behavior exercised

- The generated Node adapter runs through the real HTTP runner and H2C
  listener. Explicit promise gates remain closed while clients read response
  EOF over HTTP/1 and HTTP/2 for buffered, streamed and Fetch responses.
  A neighbouring invocation completes in the same process with its own ID
  and abort signal, while the held task remains counted.
- Registered children remain owned after their parent settles. A rejected
  task records failure without changing the completed response or killing a
  neighbour. Completion of the HTTP owner's request context does not cancel
  post-response promises.
- Per-task timeout starts at registration, before response completion. A
  cooperative task retains admission through abort cleanup. A mid-stream
  client disconnect retains admission until cancellation acknowledgement.
  A task that ignores cancellation retains its slot and admission until the
  existing five-second retirement grace expires.
- A one-slot adapter negotiates request IDs and execution completion. Recycle
  admits a fresh generation while retaining a busy old worker until its tail
  actually completes. Worker death interrupts remaining work and does not
  replay an already completed HTTP request.
- All plan timeouts and capacities match the central limits table. Concurrent
  invocations share the aggregate counter. Free's four-task cap is enforced
  across interpreter processes; Scale's sixteen-task structural invocation
  cap is enforced by the generated adapter. Pre-header capacity refusal is a
  terminal `handler_error`, since the handler may already have side effects.
- Unsupported persistent adapters and one-shot handlers refuse negotiation
  before invocation side effects. Request bodies and headers cannot activate
  the runtime. Invalid/duplicate control transitions fail closed. Cancellation
  cannot be starved by buffered control frames after its deadline.
- A failed stdout pipe does not prove process exit. Both pre-existing pipe
  failure and failure during an outstanding cancellation fence force retirement
  and process wait before releasing execution ownership.

## Portable validation

Toolchains: Go 1.26.9 linux/amd64, Node 24.19.0 and 22.23.3, Python 3.12.14.
Tests used ordinary local listener/process permissions and `umask 022`.
Node 22 was obtained through npm's `node@22` package in the development cache;
no repository dependency or lockfile changed.

```sh
go test -race ./guest/runners/... ./pkg/rootfs -count=1 -timeout=3m

# With Node 22 first on PATH:
go test -race ./guest/runners/internal ./guest/runners/internal/workerpool \
  -run '^TestBackground|^TestCompletion|^TestWriteHandlerFailure' \
  -count=1 -timeout=1m

go tool golangci-lint run ./guest/runners/internal/... ./pkg/rootfs/... --timeout=5m
make adr-number-uniqueness-check runbook-sql-check text-encoding-check \
  shell-quoting-check docs-links-check
git diff --check
```

These checks passed. The complete portable command covers the Go, Node and
Python runner packages, runner parity, worker pool and rootfs tests. The new
background and pipe-failure regressions also pass with Node 22. The optional
TLS/SCRAM PostgreSQL integration is not enabled in these runs: no SQL behavior
or SDK helper changed, and no SQL fixture or credentials were created. Earlier
SQL evidence belongs to the [concurrency report](../20261016-function-backend-concurrency/REPORT.md).

The restricted command sandbox initially denied local socket creation. Allowing
local networking permits both Python's threaded event-loop wakeups and the
HTTP test listeners; this was not addressed by weakening a test or protocol.

## Native and production boundary

The strict native command was attempted during implementation:

```sh
FAAS_REQUIRE_FUNCTION_CONCURRENCY_METAL=1 \
  RUN_REGEX='^TestFunctionConcurrencyMetal$' make test-metal PKGS=./cmd/e2e
make leakcheck
```

The Go build steps completed, but execution failed its prerequisite with
`function qualification requires root on native x86_64 Linux`. This workspace
also lacks `/dev/kvm`. Leakcheck failed closed because `/dev/loop0` through
`/dev/loop7` are unavailable, so it cannot inspect the host loop devices.
Neither result is native acceptance or a passing leak qualification.

Production runner mains never construct `BackgroundRuntime`; they leave it nil.
The new process-local counters do not register host VM lifetime ownership and
do not update `instances.tail_count`. The legacy host terminal path is not
used to imply that a VM is pinned. VM-wide pool draining and checkpoint
quiescence remain unqualified. Activation requires the owner-mediated,
acknowledged registration and native gate described in
[ADR-1346](../../../adr/20261018005023-function-background-completion-protocol.md).
No production activation, deployment, durable execution, performance parity
or native snapshot acceptance is claimed.
