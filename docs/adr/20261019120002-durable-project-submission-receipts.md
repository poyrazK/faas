# ADR-20261019120002: Recover project submission acceptance after a lost response

Date: 2026-10-12
Status: Accepted

## Context

ADR-20261019120001 saves received project receipts. A dropped apply response or a client
crash before saving that response still leaves acceptance uncertain.

## Decision

The CLI saves a project attempt before POST /v1/projects, then supplies the
optional Deployment-Attempt-ID header. Default invocations use fresh opaque
attempts; explicit idempotency keys produce stable, hashed project attempt IDs.
The journal stores recovery identity, not raw keys, source or secrets.

Tracked applies reserve an account-owned project_submissions row after source
validation and before project mutation. Reusing an attempt cannot run apply
again. Changed request semantics are refused by a persisted fingerprint; secret
values participate only in that hash. Legacy requests retain existing
idempotency middleware. Tracked requests fail closed when persistence fails.

Before enqueue, the selected workload set is durably recorded. A deterministic
per-workload attempt ID binds deployment creation through ADR-20261019120049 atomic
acceptance receipts, including when the process stops before progress is written.
Reused pending deployments attach that receipt before acceptance is returned.
Current build IDs and ADR-20261019120054 diagnostics are recovered from owned deployments.
No raw request, source, arbitrary build errors or credentials enter the ledger.

GET /v1/project-submissions/{attempt_id} reads processing, complete, rejected or
interrupted state and recovered workload identities. Account authorization,
deployment read scopes and MFA protect reads. A heartbeat keeps active requests
visible; an expired heartbeat reports interrupted without mutating the row.
The selected-set marker distinguishes unknown selection from a confirmed no-op.

Resume discovers receipts using GETs, saves recovered identities, then monitors
accepted workloads through ADR-20261019120000. Unconfirmed workloads are never resubmitted.
Absent receipts remain uncertain. Refused changed-intent attempts cannot be
retagged as successful recovery. Old accepted sessions and older successful
server responses retain ADR-20261019120001 recovery compatibility.

## Consequences

A lost successful response can be recovered without source or duplicate builds.
A process crash can yield partial acceptance; callers inspect the interruption
and each workload outcome. Acceptance before build publication is reported
explicitly when publication cannot be confirmed. Receipts belong to the account
and cascade on account deletion. Request completion and acceptance remain
separate facts; receipt reads do not restart a stopped apply.

Apply the project_submissions migration before deploying updated APID, then
publish updated CLI and SDKs. The new endpoint and optional apply header are
additive. Existing cron activation and individual submission migrations remain
required. Metal lifecycle code is unchanged.
