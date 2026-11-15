# ADR-20261114232023: Reviewed project workflows in Node and Python

- **Status:** accepted
- **Date:** 2026-11-14
- **Related:** [project inspection](20261114220642-sdk-typed-project-inspection.md), [source workflows](20261114201959-sdk-resumable-source-deployment-workflows.md)

## Context

SDK callers can inspect and deploy source to an existing app, but composing a
complete project's server scan, resource reconciliation, configuration, durable
acceptance and per-service observation still requires orchestration. The cached
apply endpoint retains the reviewed archive, but bypassed project submission
tracking and therefore could not recover an uncertain apply by attempt ID.

## Decision

Add account-owned local-project workflows to both public and preview clients:
Node `projects.previewLocal`, `applyPlan`, `resumePlanApplication`; Python
`preview_project`, `apply_project_plan`, `resume_project_application`. Keep
existing generated and higher-level methods intact. Organization and source-ref
clients retain their generated interfaces; this first convenience workflow does
not introduce wrappers for them.

Prepare the complete repository using the existing bounded archive pipeline,
scan it on the server, and discard the local temporary archive. Return a typed,
immutable reviewed plan containing endpoint, project/environment, source identity,
full server preview and canonical fingerprint. Node and Python share the JSON
encoding and fingerprint algorithm. Hash finite numeric values as typed binary64
bytes (normalizing signed zero) to avoid language-specific decimal/exponent
formatting differences without changing the saved JSON. Normalize nullable Go slice fields to empty
arrays. The fingerprint detects changed intent, not an authenticated signature;
the server's opaque account-owned plan token remains authoritative.

Apply only the source retained for that token. Preserve selection, explicit app
allocations, configuration checks and protected-environment admission on the
server. Never refresh an expired/stale review implicitly. Pass initial secrets
and approval credentials only in memory. Keep saved plan tokens private.

Persist a versioned journal with endpoint, project/environment, plan fingerprint,
random 32-hex attempt identity and phase. Atomic private writes precede the
apply POST. Disable transport retries for preview and apply. Once `applying` is
durable, every restart or uncertain response uses receipt reads exclusively,
including the conservative crash window before the first POST reaches the
server. Missing receipts remain `acceptance_unknown`; callers must not infer
that a missing receipt authorizes another mutation. A new intent uses a new
review and journal. Never persist credentials, source, configuration values or
unkeyed secret hashes in the journal.

Expose an optional `Deployment-Attempt-ID` header on personal and organization
cached apply. When present, route the existing replay request through
`projectApplySubmission`, preserving its context, duplicate fence, selection
checkpoint and per-workload deployment receipts. When absent, keep the existing
cached-apply behavior. No new writer, datastore, ownership boundary or alternate
reconciliation implementation is introduced. Generate both SDKs from the updated
canonical/public specs and synchronize the embedded API spec.

Receipt records deliberately contain sparse acceptance data. Validate receipt,
project, environment and app/deployment identities, then observe accepted IDs
with existing waiters. Return aggregate and per-service outcomes, retaining
enqueue failures, unknown selection results, post-acceptance failures, unchanged
services and blocked releases. Image workloads need no build ID. A complete
receipt without expected new workloads cannot report overall success. Release
readiness is separate from application-specific data checks.

Use a single deadline for preparation, HTTP, callbacks, recovery polling and
every service waiter. Bound receipt reads, support cancellation and progress,
snapshot client configuration and reject concurrent journal writers in-process.
Separate processes must coordinate ownership of a journal externally.

## Validation

Shared Node/Python fixtures cover successful multi-service application, sparse
and processing receipts, transient response loss, source-free restart, stale and
expired reviews, admission failures, partial/interrupted selection, images,
incorrect identities, initial secrets and explicit environments/app allocations.
Additional tests cover concurrency, account isolation, configuration snapshots,
deadlines and cancellation. Installed npm/wheel consumers check runtime helpers,
generated attempt headers and strict TypeScript/mypy signatures.

A focused apid integration test verifies actual cached apply receipt creation,
duplicate fencing, legacy behavior and cross-account receipt denial. The public
journey deploys the API/worker/PostgreSQL example, stops a Node process after
acceptance, removes source, resumes in another process and reads worker-written
data through the public API. Live execution requires an explicitly configured
target; local checks compile it and verify the no-target skip.
