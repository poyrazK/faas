# ADR-1571: Durable CI execution engine qualification without compute dispatch

- Status: accepted for internal engine qualification; not launched
- Date: 2026-10-15
- Extends: ADR-1576 (internal CI control plane), ADR-099 (Jobs)

## Decision

Add a durable pipeline execution engine in `pkg/ci`, exercised against a
synthetic backend implemented exclusively in test files. Install no dispatcher,
daemon, production backend, execution API route, or CLI submission command.
`FAAS_CI_INTERNAL_ENABLED=0` and the empty account allowlist remain unchanged.
The metadata API continues to create only blocked receipts.

Use a separate account-owned `ci_executions` ledger, with mode constrained to
`qualification`. Its admission requires an explicit revision, full commit SHA,
and stable account-scoped idempotency key. The metadata and execution ledgers
have separate identities and key namespaces. There is no conversion from a
blocked receipt, automatic promotion, or scan that could start old receipts.
Qualification is synthetic evidence, not evidence of source authorization,
image availability, runtime compatibility, isolation, or compute capacity.

## Execution and recovery

The engine reconciles one execution through explicit `Engine.Tick` calls. It
admits all ready jobs in deterministic plan order, waits for dependencies to
succeed, and skips descendants of failed/cancelled/skipped dependencies.
Independent jobs continue after another branch fails. Each job has one attempt
in this milestone; there are no automatic user-command retries.

Before any external call, a transaction stores all ready-job attempt IDs,
ordered step states, start intents, and absolute deadlines. The deadline starts
at that durable intent and includes backend admission time. Recovery reuses
the same attempt ID, including after Start succeeded but its response was lost.

An execution has a 30-second controller lease, a monotonically increasing
fence, and a version. PostgreSQL supplies authoritative lease time; tests alone
inject a clock. Claim, cancellation, and result commits lock the execution row.
Expired or replaced leases cannot commit, and cancellation invalidates in-flight
results by advancing the version. The engine checks ownership before each job
and bounds the reconciliation context to the lease duration. A crashed worker's
lease can delay takeover for up to 30 seconds. No heartbeat or background loop
is installed.

The backend contract requires account ownership, idempotent Start by attempt
identity, atomic rejection of lower fences, context deadlines, and a durable
cancellation tombstone even when Cancel precedes Start. Terminal observations
certify that a task stopped. These are adapter obligations: a future Jobs
adapter must prove them before dispatch is enabled. The control plane alone
cannot make an external compute service execute a command exactly once.

Cancellation closes waiting jobs and requests cancellation of every durable
active intent. Timeouts cancel the attempt and ultimately fail its job.
Neither case reports terminal until the backend confirms it stopped. A
transport error retains intent for reconciliation with the same identity.
Terminal execution results are immutable. Explicit cancellation wins an
in-flight completion race; completed executions retain their original result.

## Persistence and output

The execution is a bounded JSON aggregate with job, attempt, and step progress,
immutable plan, deadlines, retained log prefixes, and artifact manifests.
Version/fence/lease/status are separately constrained SQL columns. All mutation
uses SQLC and row locks; this aggregate favors qualification correctness over
high-volume log ingestion. It is not a public retention or throughput promise.
The compile-time projection includes both CI migrations. The new append-only
migration can roll back without changing the metadata receipt tables.

The backend reports ordered, monotonic step progress. Unknown or regressed
states, incorrect exit codes, changed terminal results/manifests, invalid log
offsets, oversized output and undeclared artifact paths are rejected before
they can replace durable evidence. Logs use byte offsets and raw bytes (JSON
base64), 16 KiB pages and a 64 KiB retained prefix per attempt. Truncation is
explicit. Terminal tasks drain output before their attempt closes, without
restarting the task or applying a new runtime timeout to a confirmed result.

Artifact metadata can be retained after failure and cancellation. It consists
only of workspace-relative paths, SHA-256 digests and sizes. Declared selectors
use segment globs; `**` matches zero or more whole path segments. References
contain no URLs, credentials, payloads or host paths. This milestone performs no
artifact upload, source fetch, or file access. Future collection must verify
actual guest paths and account-owned objects; metadata validation is not that
proof.

Bounds in `pkg/api/limits.go`: 32 retained qualification executions per account,
4 MiB aggregate snapshots, 16 artifacts per job, 32 MiB per artifact and 64 MiB
artifact bytes per job. Existing definition/job/step bounds also apply. Admission
is serialized by the account row, and idempotent recovery works at the ceiling.
Account deletion cascades to the execution aggregate and its retained output.

## Evidence and subsequent work

Shared memory/PostgreSQL scenarios qualify a two-job pipeline, independent
branches, dependency failure, cancellation before/during Start, delayed stop,
timeout, lost Start acknowledgements, restart before receipt persistence,
stale-controller fencing, output replay/truncation, failure/cancellation
artifacts, isolation, concurrent admission/leases, bounds, and maximum graph
shape. PostgreSQL also qualifies migration rollback and cascading deletion.
The fake backend stores synthetic data and never executes pipeline commands.

Next work remains account-owned SCM bindings/triggers and then a
scheduler-owned Jobs adapter with separately disabled compute admission,
qualified images, source verification, resource/disk budgets, secret/cache
isolation, artifact storage, billing, and native teardown acceptance. apid must
continue to record intent and never call vmmd or run shell commands.

ADR-1572 subsequently adds the GitHub bindings and blocked-receipt trigger
milestone. It does not dispatch work into this qualification engine or connect
production compute.
