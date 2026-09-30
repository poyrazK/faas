# ADR-377: Gregale Issues

Status: Accepted for preview implementation, 2026-09-30.

## Context

Request debugging and HTTP error groups describe individual executions. Developers
need a durable failure identity, an owner, release history, and an explicit
resolution state. Hosting does not expose arbitrary application exceptions.

## Decision

apid owns issue intent and ingestion. Application instrumentation authenticates
with a revocable, hashed token scoped to one account, app, environment, and
deployment. The reporting credential cannot read customer data or mutate issues.
Node/Python capture helpers and bounded OTLP JSON exception adapters report
independently of Gregale's trace sampling. Existing gateway 5xx evidence enters
as a separate HTTP source; it does not claim a stack trace.

Grouping version 1 hashes source, exception type, and sanitized application
frames without line numbers or known build roots. Source-relative directories
remain significant. Explicit fingerprints support unsupported stacks. Fallback
grouping uses route, status, and normalized message. Group identity excludes
deployment but includes app and environment. No semantic clustering or generated
incident diagnosis is required to establish an issue.

One short transaction locks the app, checks credentials and quotas, deduplicates
the event, records sanitized evidence, updates issue/release counts, and appends
activity and the existing webhook outbox. Unique event identity is scoped to
app/deployment. Exact retries do not change counts or notify twice; changed
payloads using the same ID are rejected.

Assignment is restricted to the app owner or active organization members.
Resolution records an explicit fixed deployment and its immutable creation time. A later deployment can also trigger recurrence. Occurrences predating
resolution, or from an older co-serving deployment, do not reopen the issue.
Ignore expiry is an audited transition. Issue transitions use the existing
durable webhook recipient snapshot, delivery ledger, retry policy, and recovery
relay. A crash between commit and delivery is recoverable.

Customer impact counts distinct platform tenants, falling back to API consumers,
only from scoped request audit, singleton request telemetry, or immutable async
invocation admission identity. Instrumentation cannot submit customer identity.
Unknown and ambiguous evidence remains unattributed. A bounded maintenance pass
retries enrichment when request evidence arrives later. Counts describe retained,
observed events in an explicit window; they do not estimate all affected users.

All bounds live in `pkg/api/limits.go`. Preview is available on Hobby, Pro, and
Scale. Event retention is 7/30/90 days; issue and release totals survive expiry.
The maintenance loop purges occurrences and expired reporting credentials without
requiring fresh traffic. Free downgrades stop ingestion and expire detailed
occurrences. Schema rollback retains triage history.

## Consequences

Gregale adds an issue workflow to its deployment/debugging context without a
second log archive. Application capture remains bounded, best effort, and cannot
replace the application's exception semantics. SDK counters expose local drops.
Server-side rejection and persistence failures are observable. The focused
IssueStore requires PostgreSQL; unsupported store implementations fail explicitly.

Payloads exclude bodies, cookies, locals, environment variables, and arbitrary
tags. Persisted strings pass Gregale's redactor. Pattern redaction cannot guarantee
all secrets are recognized; callers must avoid embedding sensitive values.
Debugger links resolve scoped retained telemetry and show unavailable evidence
explicitly. Replay uses the existing sanitized corpus/private environment
workflow; occurrence metadata alone cannot recreate a request or external state.

## Evidence

`make test-issues` requires reachable PostgreSQL and executes production-router
acceptance, concurrent retry, customer attribution, lifecycle, webhook recovery,
OTLP, dashboard security, retention, and actual Node/Python reporter processes.
OpenAPI/SDK drift gates and generated CLI documentation cover the public surface.
No VM lifecycle is changed; live platform deployment remains a release operation.
