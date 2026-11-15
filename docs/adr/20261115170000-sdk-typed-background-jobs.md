# Typed background jobs in application SDKs

Status: accepted

## Context

The Jobs API already owns ordered input fan-out, immutable run snapshots,
admission, retries, cancellation, failed-input replay and output manifests.
Application developers still have to validate payloads, write worker glue,
recover run identities and verify result bytes themselves.

## Decision

Add portable typed job contracts, code generation, a local runner and a native
task runner to Node and Python. Add typed job handles to isolated clients;
preserve every generated Jobs method and legacy caller. Both public and preview
clients expose the additive helpers. No server API, scheduler, guest, quota or
database ownership changes are required.

A version-1 JSON contract names an existing job and defines input/output schemas.
The supported schema subset is explicit: closed objects with declared required
properties, arrays, strings, finite numbers, safe integers, booleans, null,
primitive enums, numeric bounds and length/item bounds. Unknown keywords and
unbounded schema structure are refused. Producers, local handlers, native workers
and result readers validate the same contract. Type generators emit TypeScript
types and Python TypedDicts. A canonical IEEE-754-aware fingerprint matches across
languages, independently of JSON formatting.

Inputs use the existing opaque `input_ref` field: `gregale-job:v1:` followed by a
canonical base64url JSON envelope carrying the contract fingerprint, input ID and
data. References fit the existing 2048-byte wire bound. Larger inputs use a
caller-owned immutable store with a credential-free obj/s3/gs reference, size
and SHA-256. Stored and inline inputs are bounded to 1 MiB per envelope. The SDK
does not infer storage credentials or send control-plane credentials to storage.
Store adapters receive cooperative cancellation context and must own their I/O
and credential policy. The local file adapter is for tests, uses a private
directory and maps synthetic `s3://gregale-local/<digest>` references to files.

Native workers validate the platform input identity and contract, execute a
handler, validate/store its result envelope and atomically write the existing
output manifest with one `result` artifact. Artifact bytes must be stored before
the manifest is published. The host's successful process exit remains the only
authority that publishes success. Entry points must exit unsuccessfully after a
runner error. The runtime never calls scheduler/VM internals or exits the caller's
process itself. SIGTERM handling is temporary and can be disabled by an embedding
application. Cancellation is cooperative and cannot undo committed application
writes.

Run checkpoints contain endpoint, account, job, run, contract fingerprint, ordered
input identities/binding digests, native input digest and replay lineage. They
contain no payloads, storage credentials, bearer URLs or SDK token. Loading a
checkpoint does not confer access: every server read is authenticated, exact run
and account identities are checked, and task indexes, references and replay
origins must match. A handle can resume in either language without a resubmission.

Submissions require explicit stable idempotency keys. Each mutation is sent once;
the SDK never invents a fresh key after uncertain acceptance. Missing responses,
server failures, or invalid acknowledged mutation responses use
`acceptance_unknown`. A caller can explicitly recover with the same input and key
within the server's 24-hour idempotency window. Cancellation/timeouts during a
submission can also leave an accepted run and require inspection. Checkpoint
persistence belongs to the application.

Waiting is read-only, bounded by deadline, poll count, API request count and
progress-handler budget. Cancelling a local wait does not cancel the run.
Explicit cancellation uses the existing native endpoint. Failed-only replay
creates a linked run using the server's atomic selection, retaining stable input
IDs and source task indexes. Successful inputs are excluded; the current image
must satisfy the native replay guard. Local runner retries are opt-in.

Results are complete bounded task collections, with per-item native outcomes and
validated typed outputs. A successful native task with missing, corrupt,
misattributed or schema-invalid bytes is `invalid_result`. Managed obj artifacts
use native verification and short-lived signed GETs through an independent data
client, with no inherited SDK headers/cookies, no redirects, bounded bytes and
checksum verification after download. External s3/gs references require an
explicit store adapter. Error summaries omit arbitrary server/handler text.

## Validation and consequences

Shared Node/Python fixtures cover schema fingerprints, portable references,
account isolation, interrupted submission, exact task identities, pagination,
signed downloads, corrupt results, replay and local/remote cancellation. Local
execution, runtime validation and generated consumer types are tested separately.
Generator preservation/export tests include all handwritten modules.

The CSV example uses PostgreSQL uniqueness on stable input IDs, exposes progress
and a downloadable rejection report, and demonstrates a transient worker failure
followed by failed-only replay. Local integration uses actual PostgreSQL and both
worker languages with fixture scheduling, and crosses client languages when
resuming checkpoints. The public deployed journey requires an explicitly
configured isolated matching job image and managed result adapter.

Task execution remains at least once. Applications enforce idempotent effects
with their stable input IDs; the SDK does not promise exactly-once side effects.
Store lifecycle/cleanup and application authorization remain application concerns.

Small typed inputs are encoded in the native job history’s `input_ref`. For data
that should stay in an application-owned store, model an object reference in the
input schema and fetch it in the handler. Checkpoint files retain only digests
and identities. Numeric enum values are restricted to safe integers.
