# ADR-20261114181406: Resumable SDK source uploads and acceptance recovery

- **Status:** accepted
- **Date:** 2026-11-14
- **Milestone:** SDK developer and agent integration
- **Related:** [ADR-20261114163130](20261114163130-python-sdk-build-and-deployment-waiters.md), [ADR-20261113210904](20261113210904-node-sdk-build-and-deployment-waiters.md)

## Context

The upload API already supports session creation, accepted-offset discovery,
binary append, commit and immutable deployment submission receipts. Node exposes
the raw operations but callers implement crash recovery themselves. The pinned
Python generator omits appendUpload's nonstandard binary media type. Agents need
bounded source transfer and restartable acceptance observation without creating
another deployment when a commit reply is lost.

## Decision

Add Node client.uploads.uploadSource/commitSource and Python
FaaSClient.upload_source/commit_source, inherited by preview clients. Keep generated
callers available. Transfer a prebuilt archive from a regular file; preserve upload
and commit as separate explicit actions. Return portable immutable checkpoint
state for transfer and the generated deployment acceptance receipt for commit.

Require an awaited durable onCheckpoint/on_checkpoint callback before mutation.
Checkpoints contain version, normalized API URL, app, source size/hash, metadata
fingerprint, session/offset, pending byte range, stable creation/commit keys and
one attempt ID. Stages are new, uploading, ready, committing and accepted. Exclude
auth credentials, local paths and metadata values. Provide atomic local file
helpers with private permissions and POSIX directory sync. Serialize one writer
per checkpoint; Node's asynchronous local store queues writes and reads wait for
pending saves. Other durable stores can use the same JSON representation.

Hash the archive in 1 MiB grains, then verify each grain again before buffering it
for a request. Cap payload memory at the server chunk size and 16 MiB. This detects
same-size file mutation and allows exact suffix recovery after partial acceptance.
Fingerprint metadata using tagged JSON with UTF-8 key ordering and binary64 number
encoding, including whether options were supplied, so saved state can cross SDKs.
Reject mismatched endpoint/app/source/metadata and malformed offsets or responses.
Require API authorization for the owned upload and app. Never use server-supplied
URLs or follow redirects to send archives or recover acceptance.

Capture client settings, source path, metadata and policies at invocation. Require
one positive total deadline across hashing, requests and awaited callbacks. Check
monotonic expiry before requests, including after synchronous callbacks. Preserve
native caller cancellation, stop future requests after late custom work, and
retain connection-pool ownership on the Python facade. Source read threads own
and close their descriptors independently of caller cancellation.

Disable automatic HTTP retries for writes. Use a stable key to recover a lost
session creation reply. Persist the exact in-flight range before PATCH; after an
ambiguous/conflicting write, discover the accepted offset before retrying bytes.
Bound these recoveries (default 3, configurable 0–100), and retain existing policy
for GET requests. Expired and cancelled sessions stop without implicit replacement.
Expose frozen progress and typed SourceUploadError outcomes, checkpoint and cause;
Python total timeouts also derive from RequestTimeoutError and TimeoutError.

Commit first checks the immutable acceptance receipt. A ready, complete session
persists committing before one POST with its saved attempt ID and commit key.
After any reply, including an error, query the receipt. A committing/accepted
restart only reads acceptance; a missing receipt is acceptance_unknown and never
permits automatic resubmission. This conservative outcome also covers interruption
between durable intent and sending the request. Keep the original cause when a
commit reply failed. Receipt identity must match the saved attempt and any known
deployment. Existing release waiters can observe the returned deployment ID.

Normalize appendUpload's request media type for code generation only. A fail-closed
postprocessor restores application/offset+octet-stream, accepts File or bytes,
reads a File payload before async HTTPX, and exposes typed detailed headers. Keep
the handwritten modules, export barrels and tests through generation.

## Consequences and verification

Developers and agents can persist source progress, reconnect from another process,
and distinguish transfer completion from deployment acceptance. Source packaging,
release approval, promotion, rollback and application health remain explicit.

Shared public/preview Node/Python scenarios verify source/metadata binding, durable
restart, bounded byte-range recovery, lost creation/chunk/commit replies, pending
or missing receipts, no duplicate commit, cancellation, expiry, storage failure,
malformed responses and recovery caps. Add real HTTP concurrency and ownership,
late-callback/deadline checks, bounded file reads, generated binary requests,
installed-package runtime and typing, generator preservation, and source mirrors.
