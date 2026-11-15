# ADR-20261114201959: Resumable source deployment workflows in the SDKs

- **Status:** accepted
- **Date:** 2026-11-14
- **Milestone:** SDK developer and agent integration
- **Related:** [ADR-20261114193000](20261114193000-sdk-local-source-preparation.md), [ADR-20261114181406](20261114181406-sdk-resumable-source-uploads.md)

## Context

Node and Python can prepare local source, transfer it with durable checkpoints,
recover deployment acceptance and observe release timelines. Callers still write
the orchestration, preserve archives and interpret partial failures. Restarting
that orchestration incorrectly can create a new deployment instead of finding
the accepted one.

## Decision

Add isolated Node `client.deployments.deploySource`/`resumeSourceDeployment` and
Python `FaaSClient.deploy_source`/`resume_source_deployment`. Both public and
preview clients expose the helpers. Existing generated, static and low-level
interfaces keep their behavior. Compose the existing preparation, source upload,
explicit commit and deployment waiter implementations for an existing app.
The workflow call explicitly authorizes source submission; server release
approval, scheduling, hold and traffic policies remain authoritative.

Require a local state path and a positive total deadline. Resolve the state
parent and keep it outside the source context. Store a versioned private JSON
journal with workflow ID, endpoint, app, original directory, metadata and source
selection fingerprints, archive size/hash and the existing upload checkpoint.
Never persist credentials, deployment option values or release logs. The archive
has a generated workflow-owned name beside the journal. Read at most 64 KiB of
strictly validated state; atomic writes use 0600 temporary files, file sync,
rename and POSIX parent directory sync.

Save initial state before preparation and archive identity before transfer. Save
stable upload creation/attempt/commit identities from that archive before any
remote write. Reuse the existing checkpointed transfer and persist every new
checkpoint before its mutation. Once the upload is committing or accepted,
resume skips both source and archive reads and invokes receipt recovery only.
Missing receipts remain `acceptance_unknown`, including a crash after saving
committing state but before sending commit. Never replace that uncertainty with
a fresh attempt, repeat commit or automatic new session. Each intentionally new
deployment uses a new journal path.

Restart reads and validates the same journal. Match endpoint, app, option
fingerprint and preparation policy; require the original directory spelling
when using deploySource again. Explicit resume uses the saved directory and
fails if the journal is missing. Prepared archives are reused even when the
worktree changes. Before committing, the upload helper verifies the archive's
saved identity. After acceptance, always observe the saved deployment ID again
rather than replaying mutations or trusting a cached success.

Return typed succeeded/blocked/failed resource outcomes with receipt, release
timeline and diagnostic code. Operational failures carry code, phase, state
path, saved state and original cause. Python caller cancellation remains an
asyncio.CancelledError subtype with the caller's message. Deadlines, local
cancellation and progress callbacks span the whole invocation, with one
monotonic budget passed through every child operation. Retry/recovery and poll
limits remain explicit. Capture client configuration, metadata and options at
call time. Python snapshots borrow the original transport policy owner without
closing its pool.

Allow independent journals to run concurrently. Reject simultaneous writers to
one canonical journal path within a process. Callers coordinate one writer
across processes; this local journal is not a distributed lease. Retain the gate
through pending local writes, retire late callbacks and stop child operations
before allowing further remote work. Local cancellation never cancels an
accepted deployment on the server.

## Verification

Use shared Node/Python phase-interruption and recovery fixtures, with matching
fingerprints, public/preview surfaces, changed archives, absent original source,
lost chunk/commit replies, unknown acceptance, blockers, resource failures and
poll caps. Test actual process exits with committing state, concurrent accounts,
client changes, caller cancellation, callback deadlines, invalid policies and
corrupt journals. Re-run lower-level request/upload/waiter compatibility suites.
Exercise installed npm/wheel APIs and strict result types. Keep handwritten
Python modules and both barrels reproducible through generation.

## Consequences

An application or agent can deploy a directory and later resume the same intent
without implementing orchestration or accidentally submitting another deployment.
The first version uses local artifacts and journals, explicit source roots and
the platform's current release policies. Distributed workflow storage and writer
coordination remain outside this interface.
