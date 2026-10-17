# Data API load and recovery verifier

Run concurrent REST/RPC traffic and controlled faults against a disposable local
PostgreSQL 16 database and the current embedded gateway. This is local fixture
evidence. Hosted capacity, native VM lifecycle, production process supervision,
distributed quotas and SQL execution/pool-wait timing require separate evidence.

## Run

Use Node 22 or newer on Linux x86_64. Supply a loopback administrative PostgreSQL
16 URL with permission to create/drop databases and roles. The existing database
is only the control connection; the verifier creates its own database and role.
Only the optional `sslmode` URL query parameter is accepted; connection-target
overrides are rejected.
Supply the official PostgREST 14.3 static Linux x86_64 executable. Its size and
SHA-256 are verified **before executing it**:

```text
6fc07c4b166d54dabd5f2d2d5ba6e1f950385baaab0e5e24d29215e030a86839
```

```sh
export DATA_API_TEST_DATABASE_URL='postgres://fixture@127.0.0.1:5432/fixture'
export DATA_API_POSTGREST_BIN=/absolute/path/to/postgrest
export DATA_API_LOAD_REPORT=/absolute/path/to/load-report.json
make data-api-load-check
make data-api-load-verify
# Optional bounded workload:
bash scripts/test-data-api-load.sh --requests 160 --concurrency 32 --users 16
```

The wrapper copies runtime sources to a temporary directory, installs their
locked dependencies there, and builds the local SDK. It does not install under
embedded templates. No CLI build, customer image build, deployment or package
publication is involved. Prepared environments can instead set
`DATA_API_RUNTIME_DIR` to such a dependency-installed copy, build `sdk/data/dist`,
and invoke `node tests/data-api/load/verify.mjs --output /absolute/report.json`.

Default mixed traffic is 120 requests, eight workers and four users, both before
and after faults. Bounds in `pkg/api/limits.go` permit 1–1,000 requests, 1–32
workers and 2–16 users, with at least four requests per user. Per-user REST/RPC
RLS probes precede each mixed phase. Dedicated fault phases use their own fixed
pressure independent of the mixed worker count. Run/scenario/cleanup deadlines
are 180/45/30 seconds; captured records are capped at 4,096 and reports at 64 KiB.

## Checks

1. Mixed signed-user REST reads/inserts and RPC reads/writes; inspect accepted
   and rejected writes independently through the owner connection.
2. Hold SQL locks to fill two active slots and eight waiters. Verify queue-full,
   expiry and cancellation, liveness, degraded readiness and recovery. Rejected
   queued writes must be absent from SQL.
3. Enable fixture rates of two requests/second with burst eight. Observe stable
   429 rejections, another user's progress and refill recovery.
4. Hold an RPC write beyond the actual 15-second HTTP deadline while another
   user progresses, then settle SQL and reconcile its receipt.
5. Disconnect an in-flight write, settle SQL and reconcile before any explicit
   same-key replay. At most one row may exist; the SDK makes no automatic retry.
6. Exercise responses above 16 MiB through REST and a committing RPC. Verify
   response-limit logs, unaffected reads and the committed write's receipt.
7. Discard a successful write response after headers, then require its receipt
   without replay. This deliberately simulates loss of the client reply.
8. Kill the engine during observed SQL work; recreate the embedded gateway and
   engine, require readiness, preserved rows and a successful authenticated read.
9. Repeat mixed traffic and RLS checks after recovery.

Expected rate, admission, deadline and transport failures are counted and can
belong to a passing scenario. Unexpected outcomes stop later phases. The fixture
uses real ES256 verification with a local public key; it does not qualify remote
JWKS fetching. Its serving role's SQL timeout is intentionally 30 seconds so the
15-second HTTP deadline can be observed independently; deployed timeout policy
is unchanged. Engine restart recreates both embedded components and does not
exercise the runtime entry point's supervision or platform restart policy.

## Evidence and cleanup

The versioned JSON report contains source-tree and engine hashes, runtime
versions, workload/fixture policy, fixed scenario outcomes, HTTP/code counts,
client p50/p95/p99, completed/successful throughput, sampled SQL connection peaks
and recovery elapsed time. Client intervals include complete response consumption
or failure; fault durations include deliberate waits. These are measurements of
this local run, not capacity thresholds or production SLOs. SQL samples every
50 ms count only the fixture serving role, exclude owner/control connections,
and cannot prove an unsampled instantaneous maximum. Health may correctly report
unknown pool coverage while the pinned engine's SQL-dependent admin probe waits.

Reports omit DSNs, paths, JWTs, signing keys, subject/role names, row values,
request arguments, raw error bodies and child output. Atomic mode-0600
checkpoints retain completed and failed phases. Missing evidence or failed
cleanup cannot yield a successful exit. GitHub acceptance retains this redacted
report for seven days, including failed runs when a checkpoint exists.

Cleanup aborts client requests, stops the owned engine/gateway, releases owner
locks and drops only resources whose creation was acknowledged in this run.
It runs on ordinary errors and SIGINT/SIGTERM. A killed runner, host failure or
unknown CREATE acknowledgement can leave resources. Inspect the report's run ID
on the **same configured local PostgreSQL server**: owned names derive exactly as
`dapi_load_<run_id>` and `dapi_role_<run_id>`. Confirm ownership before manual
cleanup; never adopt or remove other fixture databases or roles. An unknown
creation outcome is reported as failed cleanup.

`make data-api-load-check` runs portable contracts and skips SQL tests. For real
failure/interrupt cleanup checks in a prepared runtime environment, run:

```sh
DATA_API_LOAD_FAILURE_TESTS=1 node --test tests/data-api/load/integration.test.mjs
```

`make data-api-acceptance` includes the full verifier and these real cleanup
tests. The runner's request/control interface and evidence machinery also support
a [hosted staging adapter](../staging/README.md#hosted-load-and-recovery).
It has seven explicitly supported scenarios and marks unavailable private fault
controls as unsupported. Live work requires the existing staging manifest and
qualification gates before any capability promotion.
