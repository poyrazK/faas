# ADR-20261019120001: Save accepted project deployment receipts for recovery

Date: 2026-10-12
Status: Accepted

## Context

ADR-20261019120000 waits through readiness, rollout and cron activation, but project apply
kept its workload receipt in process memory. A CLI restart or CI timeout could
lose the deployment IDs needed to reconnect to the whole accepted apply.

## Decision

After an apply response is accepted and before waiting, the CLI saves a project
session in the existing private, connection-scoped deployment session directory.
The version-1 record gains an optional project variant. It stores the project
slug/ID/environment, app identities, workload deployment/build IDs, non-acceptance
markers and cron intent identity. It never stores source, manifests, secrets,
request payloads, arbitrary server errors or a raw idempotency key. Existing
atomic file writes, directory synchronization, permissions and session locks apply.

CLI project receipts add `session_id`, `resume_command` and `recovery_saved`.
`gregale deploy resume SESSION_ID` dispatches project records before individual
app recovery. It rereads each accepted deployment and reuses ADR-20261019120000 completion
checks. Failed admissions stay visible and never cause automatic submissions.
Completed workloads remain complete, and other workloads progress independently.
Status reads verify deployment and available app identities. Old individual
sessions remain compatible; `--app` selects only individual app sessions.

The acceptance receipt is immutable recovery metadata. Resume reads current
outcomes rather than replaying cached failures or rebuilding successful apps.
A save failure emits the original accepted receipt with `recovery_saved:false`
and exits nonzero, preserving IDs for manual inspection. `--no-wait` also saves
its receipt and returns immediately. Timeout/interruption keeps the session.

## Limits

This covers accepted responses received by the client. A lost apply response or
a crash before the initial receipt is saved remains outside this recovery
contract; recovering that gap requires durable server-side project acceptance
tracking. Resume never uploads source, resubmits apply, overwrites configuration
or retries workloads that failed admission. No API, SDK or migration changes are
needed; additional receipt fields belong to the CLI only.
