# ADR-573: Customer-requested continuation of failed workflow runs

## Status

Accepted — 2026-10-03.

## Context

Automatic retries and lease recovery handle temporary errors and interrupted
workers. They leave exhausted runs terminal. Starting a new run reexecutes
successful actions, including the completed prefix of an invoice batch. Customers
need to continue after correcting integration credentials or a provider outage.

## Decision

Expose `POST /v1/workflows/runs/{id}/resume` with a required
`expected_resume_count`, and `GET /v1/workflows/runs/{id}/resumes`. The write uses
normal account ownership, MFA, deployment-write scopes, runtime and plan gates,
request idempotency and active-run admission. Read access uses the normal read
scope. Resume count starts at zero and is bounded to 16, centralized in
`pkg/api/limits.go`. Request bodies are bounded to 4096 bytes.

A resume keeps the same run ID, definition snapshot, input, successful outputs,
guard decisions and action idempotency keys. It reopens failed executor actions,
initialized failed iteration parents and descendants skipped because of their
failures. Guard-false and untaken-route branches remain skipped. Batch inputs
and successful items remain stored; remaining items resume in their original
order. A publication or app deployment does not replace the captured workflow.
App handlers are served by the current live default deployment as with retries.

Failed steps gain `retry_base = attempt`. Attempt numbers remain monotonic,
and the scheduler evaluates the configured retry budget and backoff relative
to this base. Resume never deletes or rewrites attempt history. For resumed
runs, interrupted app actions consume their attempt and are closed as unknown
rather than reusing an old attempt row. Action identities remain unchanged;
app handlers must deduplicate their Idempotency-Key across retries and resumes.
Managed integration mutations require declared provider idempotency. GET/HEAD
are replayable. Provider deduplication must remain valid over the full retry
and resume interval; the existing declaration does not model provider key TTLs.

Only failed/dead runs with persisted actionable failures are eligible.
Cancellation has a durable timestamp; legacy operator-cancelled errors are
also blocked. Active calls, running attempt records and parked waits block
resume. Failed control steps, failures before an executor attempt, incomplete
batch metadata, replay-unsafe integration mutations and already activated
failure/timeout handlers or their descendants are rejected. These restrictions
avoid restarting expired waits, repeating compensation or replaying an action
with unknown effects. Fixing a bad definition requires publishing a correction
and starting a new run. This first version provides no arbitrary step selection,
input override, changed-definition replay or override of replay protections.

The store locks app admission, current account/app eligibility and the run.
It checks the live deployment, current plan and all managed integration
bindings, validates the captured DAG, computes continuation from stored steps,
resets eligible rows, records the previous status/error, requesting account,
reopened names and timestamp, and queues the run in one transaction. The
revision check makes concurrent requests have one winner. Request-idempotency
replays return the original accepted response. Resume does not change trigger
receipts or schedule cursors. History is bounded and follows run retention
through a cascading foreign key.

A scheduler invocation pins the first observed resume generation in context.
Run, step, control and wait transitions check that generation while holding
the existing run lock or as an atomic update predicate. Resumed executor starts,
completions and retries additionally require the live run lease and current
attempt; app calls monitor the existing attempt nonce to stop on cancellation
or recovery. This fences workers from an earlier generation after an API resume.
The API queues intent through the state store; schedd still dispatches actions,
and outboundd still authorizes requests and injects sealed credentials.

## Rollout and rollback

Apply `20261003180000001_workflow_resume.sql` before the updated apid, schedd and
outboundd. Update/drain scheduler workers before enabling use of the endpoint;
older workers cannot observe resume-generation fences or relative retry budgets.
No runtime flags or production migrations are changed by this implementation.

Before downgrade, stop admitting new resumes, drain/cancel resumed runs and stop
updated binaries. Export resume history if needed. The down migration removes
resume history and metadata; retained attempt rows still use monotonic numbers.
Older schedulers must not process unfinished resumed runs after that downgrade.

## Validation

Memory/PostgreSQL store tests cover atomic admission, concurrent requests,
ownership, eligibility, guard/input/output preservation, retry history, stale
starts/completions/retries, lease recovery and replay restrictions. Scheduler
tests cover failed batch continuation and renewed retry budgets with unchanged
payloads/keys. API and Go/Node/Python SDK tests cover revision zero, idempotent
replays, bounded request bodies, ownership, history and retry inspection.
