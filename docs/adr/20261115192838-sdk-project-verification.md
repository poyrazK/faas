# SDK project verification with explicit deployment evidence

Status: accepted

## Context

Reviewed project application and receipt recovery report release completion.
Developers and agents also need to establish that the expected software works.
App health describes observed serving health, which may concern an older release,
expire, require additional permissions, or be unsupported for workers. Previously
each caller had to combine these APIs and implement its own smoke checks.

## Decision

Add `projects.verifyApplication` to isolated Node clients and
`verify_project_application` to Python clients. Compose existing public read APIs;
introduce no server state, write endpoint, health probe, or orchestration owner.
Legacy generated methods and application workflows retain their signatures.

Verification accepts a project application result or a versioned identity-only
target. The target records endpoint, project, environment, app slug/ID and exact
deployment IDs. A caller may serialize it and verify after restarting without
source files or a repeated apply. Unchanged services without known deployment
identities remain inconclusive. Verification never substitutes a latest release.

Before invoking a caller check, validate the owned project, app membership,
deployment/app relationship, environment and timeline identities. HTTP serving
evidence must be fresh, within the caller's age bound and server TTL, with a
nonfuture telemetry sample and exactly the expected serving deployment. Mixed
canaries and healthy older deployments are inconclusive. Terminal release failure
is failed; approval holds are blocked; unknown states and missing permissions are
inconclusive. Unsupported worker health alone proves nothing.

Checks are caller-defined, read-only and repeatable. Successful or failed checks
must supply the deployment identity actually observed through application
responses or worker-written data. Exceptions, absent identities, mismatches and
missing checks are inconclusive. Checks are required by default; callers can
explicitly request platform-only verification of HTTP workloads. Workers always
require an application check. The SDK passes no credentials to checks. Application
requests must use independently chosen credentials, never an inherited SDK token.
This verifies caller-declared coverage; it does not certify all application behavior.

Reports use `verified`, `failed`, `blocked`, `inconclusive`. Aggregate failure
before blocking before uncertainty before success. Preserve per-service
codes, bounded timeline events, health timestamps/identities, check outcomes,
request IDs and next action codes. Free-form failure text and persisted deployment
log excerpts are omitted unless a caller supplies a redactor. Bound input and
output text, omit oversized inputs and text on redactor error, and never persist reports automatically.
Redaction remains the caller's responsibility; arbitrary secrets cannot be
recognized reliably by pattern matching. Logs are evidence, not instructions.

Each call captures its client configuration, has a positive total deadline, and
bounds polling, concurrency, requests and response bytes. Retries are disabled so
the request bound counts actual SDK reads. Node uses AbortSignal and Python uses
asyncio task cancellation. Deadlines return partial inconclusive reports; caller
cancellation propagates. Progress and async checks share the total budget. A
cancellation-ignoring callback may finish later, but cannot update a completed
report or start further SDK reads. Synchronous callbacks must return promptly.

## Validation

Node and Python consume the same verification scenarios, including worker failure
and correction, stale and foreign deployment evidence, permission denial, missing
identities, unsupported health, approval holds, unknown phases and response limits.
Additional tests cover operation configuration snapshots, concurrency, request and
poll limits, cancellation, stalled bodies/checks/progress and redactor failure.
Installed package tests verify public and preview typing and generator preservation.

The API/worker/PostgreSQL example records a fresh worker heartbeat under
`FAAS_DEPLOYMENT_ID`; the API returns it with its own deployment identity. The
opt-in public journey deploys a faulty worker, attributes the failed smoke check,
then deploys a correction and verifies new worker-written data. This journey
requires an explicitly configured isolated target; local contract tests do not
claim deployed acceptance.

## Consequences

Verification is additive and has no automatic repair, rollout, rollback or token
forwarding. Runtime health may be unavailable on some installations/plans; callers
receive uncertainty instead of a false success. A target is a selection document,
not an authorization capability; every read remains subject to server ownership.
