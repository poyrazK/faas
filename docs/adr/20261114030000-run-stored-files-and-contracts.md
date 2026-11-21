# ADR 20261114030000 — Stored Run files and standalone contracts

Status: implemented behind the disabled-by-default preview writer; bounded
Node 24 functional qualification passed. Existing native release gates remain
in force.

## Decision

Extend disposable Runs with independently budgeted stored file inputs/outputs
and standalone input/result JSON Schema contracts. Preserve the existing inline
artifact contract, short overall deadline, fresh VM per Run, payload-free
template capture and physical teardown before terminal acknowledgement.
No guest receives storage credentials, object-store URLs or a host directory.

Extended requests use `POST /v2/executions`; ordinary requests retain
`POST /v1/executions`. SDKs choose v2 for schemas, stored inputs or stored
outputs and never retry through v1 when v2 is unavailable. An older apid must
reject the new route instead of ignoring new request fields and running code
without its required files or contracts. Receipts/events retain their existing
resource URLs and API-key-family ownership. Admission identities continue to
bind the complete immutable logical request before preparation and quota use.

## Standalone contracts

`input_schema` and `result_schema` use the existing workflow validator:
JSON Schema Draft 2020-12, local references only, existing complexity checks,
and a central 64 KiB bound per schema. Validate input before durable admission
or quota/grant consumption. Never expose input values or schema constants in
validation errors. Input schemas need not cross the guest boundary.

Seal the result schema with the source/input in payload envelope version 2.
An old scheduler refuses that version before dispatching user code. After
execution, the host validates the original JSON result against that immutable
contract. A schema violation preserves execution status, result, artifacts
and usage, and records separate `result_validation.status = invalid` metadata.
CLI waits return nonzero and workflow dependents treat it as contract failure.
Neither validation nor recovery automatically re-executes customer code.

The nullable `executions.result_validation` column has a closed shape CHECK.
Only successful execution can contain validation; omitted contracts retain
legacy receipts. Terminalization, validation metadata, payload erasure, events
and the single execution usage entry remain one transaction.

## Stored data files

Use a dedicated private object namespace and metadata ledger. File IDs are
locators, not bearer capabilities. Every metadata, upload and download request
checks account and Runs-key-family access; existing one-time artifact grants
remain the explicit boundary for cross-family handoff.

Uploads reserve bounded account storage/count before streaming, authenticate
length and SHA-256, encrypt at rest and become readable only after immutable
publication. Uploaded file identity is stable for the same account, key family,
digest and size, so retrying a CLI submission does not change its logical
request or consume storage quota twice. Failed/uncertain writes retain enough
metadata for reconciliation; deleting a local receipt is never storage GC.

Each object uses an independent age key. Wrap that key under the account seal
domain, bound to account, file ID and storage key. Admission unwraps authorized
file keys and seals them inside the delivery-domain execution payload. Only
after fresh restore does schedd open that payload and pass scoped file metadata
and keys over the private vmmd execution transport. vmmd handles object I/O;
the guest sees only file descriptors, bytes and opaque IDs. Raw object keys
and identities must never enter guest JSON, public receipts, events or logs.

Reserve output budget and durable output intents atomically with Run admission.
An output intent records its private immutable object key and wrapped key before
the VM can publish bytes, so scheduler death cannot strand an undiscoverable
object. Input links protect admitted data from expiry/GC. Ready output metadata
commits with terminalization. Failed output intents retain their storage
reservation until GC confirms deletion, preventing repeated failed Runs from
bypassing account capacity.

## Streaming and lifecycle

Use an additive execution-files RPC and guest protocol version 4. Old vmmds
return Unimplemented; old guests reject the version. There is no legacy retry
after uncertain dispatch. Transfer file data in bounded chunks with independent
input/output budgets, backpressure, cancellation and checksums. No large file
is materialized in an RPC message or the receipt's JSON/base64 output budget.
Metadata still counts against the existing result/log budget.

Stage inputs only in the current guest's ephemeral input directory. Stored
artifacts use input_files; legacy artifact_inputs continues to copy inline
files into its documented bundle destination. Reject traversal,
symlinks, special files, duplicate paths and source/control-file collisions.
Collect only explicitly selected regular output files after interpreter exit.
Validate guest declarations against host-observed sizes/digests and the
preallocated output IDs. Join all transfer workers before acknowledgement.

Keep file, account storage/count, TTL, transfer chunk, upload deadline, cleanup
grace and scratch headroom limits in `pkg/api/limits.go`. Stored bytes have a
separate budget from source, JSON input and stdout/result. Admission must fit
data plus output allowance within the selected scratch class. File transfer
does not extend the immutable Run deadline. Explicit retention is independent
of the lifetime of the disposable VM.

GC claims only expired/revoked/failed objects with no live input reference and
no active producing Run or upload. Preserve reservations on uncertain deletion
and retry idempotently. Disable new v2 admissions before rollback; retain file
readers, metadata, wrapped keys, configured storage and GC while data remains.
Deploy schema and compatible consumers before enabling new writers.

The public router reserves the two v2 Run endpoint roots on platform hosts;
tenant hosts retain those paths. Binary file upload/download requests use a
separate two-minute request budget, including authentication and storage work,
instead of inheriting the ordinary five-second API budget. Metadata and Run
admission retain their existing budgets. Transfers preserve caller cancellation
and earlier parent deadlines, and set socket read/write deadlines so a stalled
peer cannot pin a handler after cancellation.

## Acceptance

Require API/CLI/SDK coverage for upload, large input, stored output, authenticated
download, expiry, cross-family refusal/grants, concurrent quota, lost admission
acknowledgements, corruption, cancellation and cleanup. Verify valid, invalid,
unsupported and oversized schemas, unchanged execution status on contract
failure, nonzero CLI waits and blocked dependents. Test chunk backpressure and
joined cancellation with real transports, then run the complete journey on the
selected internal KVM fixture with physical leak checks. Its virtualized host
still supplies functional evidence only; this increment grants no native
production eligibility.

## Seal-domain and rollout details

Register execution_files.sealed_key in the Account domain. No daemon gains a
new persistent private identity. A single fileIdentity helper validates the
account/file/storage binding and parses a delegated per-object age key in
memory. The seal-domain scanner exempts only that parse call in that helper;
other functions and persistent credential loaders remain checked, and the
baseline does not grow. vmmd validates these references before guest dispatch.

Upload preparation is stateless and reserves no quota. The CLI probes the Run
admission identity before uploading missing data, then retries only the explicit
files-not-ready rejection under the same key. That rejection commits no Run.
Completed output reservations and ready sizes are measured in one SQL statement
snapshot so completion cannot create an undercount between separate reads.

Guest input/output/source scratch directories live on the existing writable
block-device filesystem, not the RAM-backed /tmp mount. Keep the two-drive
layout and cold path. Stored transfers remain within the immutable deadline.

File ledger rows intentionally outlive account/receipt deletion: account and
producer IDs are immutable scope values, without cascading foreign keys. Live
input links cascade with deleted executions. Account deletion immediately
removes authenticated access; GC recognizes a missing owner, observes existing
transfer leases and grace, deletes the object and only then erases its private
ledger/key. This avoids blocking account deletion or stranding untracked blobs.
